package icloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

type SecurityQuestion struct {
	ID       int    `json:"id"`
	Question string `json:"question"`
	Number   *int   `json:"number,omitempty"`
}
type SecurityQuestionAnswer struct {
	ID     int    `json:"id"`
	Answer string `json:"answer"`
}
type SecurityQuestionStatus struct {
	SecurityQuestion
	Saved bool `json:"saved"`
}
type SecurityQuestionsStatus struct {
	Questions     []SecurityQuestionStatus `json:"questions"`
	Blocked       bool                     `json:"blocked"`
	BlockedReason string                   `json:"blocked_reason,omitempty"`
}

var ErrSecurityAnswersMissing = errors.New("请在面板“安全提示问题”中填写并加密保存 Apple 当前要求的答案")
var ErrSecurityAnswersRejected = errors.New("Apple 未接受安全问题答案，已暂停自动提交；请在面板修改答案后再验证")
var ErrSecurityAdditionalVerification = errors.New("Apple 要求后续账号安全验证")
var ErrSecurityVerificationPaused = errors.New("安全问题验证已暂停")

// Providers must bind answers to the authenticated account, persist rejection
// across restarts, and return exactly the IDs requested by Apple's challenge.
type SecurityQuestionsProvider interface {
	Answers(context.Context, []SecurityQuestion) ([]SecurityQuestionAnswer, error)
	Pause(context.Context, string) error
}

type questionAuthRequest func(context.Context, *hmeAuthState, string, string, []byte, http.Header) (*http.Response, []byte, error)

func securityQuestionChallenge(status int, data []byte) bool {
	var result struct {
		AuthType string `json:"authType"`
	}
	return (status == 412 || status == 409) && json.Unmarshal(data, &result) == nil && result.AuthType == "sa"
}

func parseSecurityQuestions(data []byte) ([]SecurityQuestion, error) {
	var boot struct {
		Direct struct {
			TwoSV struct {
				SecurityQuestions struct {
					Questions []SecurityQuestion `json:"questions"`
				} `json:"securityQuestions"`
			} `json:"twoSV"`
		} `json:"direct"`
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法读取 Apple 安全问题")
	}
	var blocks int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			for _, attr := range n.Attr {
				if attr.Key == "class" && strings.Contains(" "+attr.Val+" ", " boot_args ") && n.FirstChild != nil {
					blocks++
					_ = json.Unmarshal([]byte(n.FirstChild.Data), &boot)
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	questions := boot.Direct.TwoSV.SecurityQuestions.Questions
	if blocks != 1 || len(questions) == 0 || len(questions) > 3 {
		return nil, errors.New("Apple 未返回可确认的安全问题，未提交答案")
	}
	seen := map[int]bool{}
	for _, q := range questions {
		if q.ID <= 0 || q.ID > 1000000 || seen[q.ID] || strings.TrimSpace(q.Question) == "" || len(q.Question) > 1024 {
			return nil, errors.New("Apple 安全问题编号或内容不完整")
		}
		seen[q.ID] = true
	}
	return questions, nil
}

func answerSecurityQuestions(ctx context.Context, base string, state *hmeAuthState, request questionAuthRequest, headers func(*hmeAuthState) http.Header, provider SecurityQuestionsProvider) error {
	h := headers(state)
	h.Set("Accept", "text/html")
	response, data, err := request(ctx, state, http.MethodGet, base, nil, h)
	if err != nil || (response.StatusCode != 200 && response.StatusCode != 412) {
		return errors.New("Apple 安全问题读取失败")
	}
	questions, err := parseSecurityQuestions(data)
	if err != nil {
		return err
	}
	if provider == nil {
		return ErrSecurityAnswersMissing
	}
	answers, err := provider.Answers(ctx, questions)
	if err != nil {
		return err
	}
	if len(answers) != len(questions) {
		return ErrSecurityAnswersMissing
	}
	byID := map[int]string{}
	for _, a := range answers {
		if byID[a.ID] != "" || strings.TrimSpace(a.Answer) == "" || len(a.Answer) > 512 {
			return ErrSecurityAnswersMissing
		}
		byID[a.ID] = a.Answer
	}
	type submission struct {
		SecurityQuestion
		Answer string `json:"answer"`
	}
	ordered := make([]submission, 0, len(questions))
	for _, q := range questions {
		if byID[q.ID] == "" {
			return ErrSecurityAnswersMissing
		}
		ordered = append(ordered, submission{SecurityQuestion: q, Answer: byID[q.ID]})
	}
	body, _ := json.Marshal(map[string]any{"questions": ordered})
	defer clear(body)
	response, _, err = request(ctx, state, http.MethodPost, base+"/verify/questions", body, headers(state))
	// One submission per challenge. Even ambiguous failures pause automatic use
	// until the user updates the stored answers, preventing lockout loops.
	if err != nil || (response.StatusCode != 200 && response.StatusCode != 204 && response.StatusCode != 250) {
		reason := "unconfirmed"
		if err == nil {
			switch response.StatusCode {
			case 400, 401:
				reason = "rejected"
			case 409, 412:
				reason = "additional-verification"
			case 423, 429:
				reason = "locked"
			}
		}
		if markErr := provider.Pause(ctx, reason); markErr != nil {
			return errors.New("安全问题验证未完成，且无法保存暂停状态；请停止后台检查本机存储")
		}
		if err == nil {
			return &SecurityQuestionVerificationError{HTTPStatus: response.StatusCode}
		}
		return errors.New("安全问题提交结果未确认，已暂停自动提交；请稍后检查账号验证状态")
	}
	return nil
}

type SecurityQuestionVerificationError struct{ HTTPStatus int }

func (e *SecurityQuestionVerificationError) Error() string {
	if e.HTTPStatus == 409 || e.HTTPStatus == 412 {
		return fmt.Sprintf("Apple 要求继续完成账号安全验证（HTTP %d）；答案已保存，请先在 Apple 账户页面查看后续要求", e.HTTPStatus)
	}
	if e.HTTPStatus == 423 || e.HTTPStatus == 429 {
		return fmt.Sprintf("Apple 暂时限制安全问题验证（HTTP %d）；请等待限制解除后再检查", e.HTTPStatus)
	}
	if e.HTTPStatus != 400 && e.HTTPStatus != 401 {
		return fmt.Sprintf("安全问题验证未完成（HTTP %d），已暂停自动提交，请稍后检查", e.HTTPStatus)
	}
	return fmt.Sprintf("%s（HTTP %d）", ErrSecurityAnswersRejected.Error(), e.HTTPStatus)
}
func (e *SecurityQuestionVerificationError) Unwrap() error {
	if e.HTTPStatus == 400 || e.HTTPStatus == 401 {
		return ErrSecurityAnswersRejected
	}
	if e.HTTPStatus == 409 || e.HTTPStatus == 412 {
		return ErrSecurityAdditionalVerification
	}
	return ErrSecurityVerificationPaused
}
