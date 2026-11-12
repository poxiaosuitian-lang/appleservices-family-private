package icloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const questionBoot = `<script type="application/json" class="boot_args">{"direct":{"twoSV":{"securityQuestions":{"questions":[{"id":142,"number":2,"question":"Question two?"},{"id":136,"number":1,"question":"Question one?"}]}}}}</script>`

type testQuestionProvider struct {
	answers  []SecurityQuestionAnswer
	rejected bool
	failure  error
}

func (p *testQuestionProvider) Answers(context.Context, []SecurityQuestion) ([]SecurityQuestionAnswer, error) {
	return p.answers, p.failure
}
func (p *testQuestionProvider) Pause(_ context.Context, reason string) error {
	p.rejected = true
	return nil
}

func TestSecurityQuestionsUseAppleIDsAndOrder(t *testing.T) {
	p := &testQuestionProvider{answers: []SecurityQuestionAnswer{{ID: 136, Answer: "synthetic-one"}, {ID: 142, Answer: "synthetic-two"}}}
	posts := 0
	request := func(_ context.Context, _ *hmeAuthState, method, endpoint string, body []byte, h http.Header) (*http.Response, []byte, error) {
		if method == http.MethodGet {
			if h.Get("Accept") != "text/html" {
				t.Fatal("not requesting challenge")
			}
			return &http.Response{StatusCode: 412}, []byte(questionBoot), nil
		}
		posts++
		if !strings.HasSuffix(endpoint, "/auth/verify/questions") {
			t.Fatal("wrong endpoint")
		}
		var sent struct {
			Questions []struct {
				SecurityQuestion
				Answer string `json:"answer"`
			} `json:"questions"`
		}
		if json.Unmarshal(body, &sent) != nil || len(sent.Questions) != 2 || sent.Questions[0].ID != 142 || sent.Questions[0].Answer != "synthetic-two" || sent.Questions[0].Question != "Question two?" || *sent.Questions[0].Number != 2 {
			t.Fatal("answers not bound to server questions")
		}
		return &http.Response{StatusCode: 250}, nil, nil
	}
	if err := answerSecurityQuestions(context.Background(), "https://idmsa.apple.com/appleauth/auth", &hmeAuthState{}, request, func(*hmeAuthState) http.Header { return make(http.Header) }, p); err != nil || posts != 1 || p.rejected {
		t.Fatalf("verification failed: %v", err)
	}
}

func TestSecurityQuestionsMissingOrWrongIDsNeverSubmit(t *testing.T) {
	for _, answers := range [][]SecurityQuestionAnswer{nil, {{ID: 136, Answer: "test"}}, {{ID: 136, Answer: "test"}, {ID: 999, Answer: "test"}}, {{ID: 136, Answer: "test"}, {ID: 136, Answer: "test"}}} {
		p := &testQuestionProvider{answers: answers}
		req := func(_ context.Context, _ *hmeAuthState, method, _ string, _ []byte, _ http.Header) (*http.Response, []byte, error) {
			if method != http.MethodGet {
				t.Fatal("submitted incomplete answers")
			}
			return &http.Response{StatusCode: 412}, []byte(questionBoot), nil
		}
		err := answerSecurityQuestions(context.Background(), "https://idmsa.apple.com/appleauth/auth", &hmeAuthState{}, req, func(*hmeAuthState) http.Header { return make(http.Header) }, p)
		if !errors.Is(err, ErrSecurityAnswersMissing) {
			t.Fatal("missing answer not explained")
		}
	}
}

func TestSecurityQuestionsRejectedPausesWithoutRetry(t *testing.T) {
	for _, status := range []int{400, 401, 409, 412, 429, 503} {
		p := &testQuestionProvider{answers: []SecurityQuestionAnswer{{ID: 136, Answer: "test"}, {ID: 142, Answer: "test"}}}
		posts := 0
		req := func(_ context.Context, _ *hmeAuthState, method, _ string, _ []byte, _ http.Header) (*http.Response, []byte, error) {
			if method == http.MethodGet {
				return &http.Response{StatusCode: 412}, []byte(questionBoot), nil
			}
			posts++
			return &http.Response{StatusCode: status}, []byte(`{"message":"private-response"}`), nil
		}
		err := answerSecurityQuestions(context.Background(), "https://idmsa.apple.com/appleauth/auth", &hmeAuthState{}, req, func(*hmeAuthState) http.Header { return make(http.Header) }, p)
		var detail *SecurityQuestionVerificationError
		if !errors.As(err, &detail) || detail.HTTPStatus != status || posts != 1 || !p.rejected || strings.Contains(err.Error(), "private-response") {
			t.Fatal("rejection was retried, leaked or not paused")
		}
		if (status == 409 || status == 412) && (errors.Is(err, ErrSecurityAnswersRejected) || !errors.Is(err, ErrSecurityAdditionalVerification)) {
			t.Fatal("additional verification classified as wrong answers")
		}
	}
}

func TestSecurityQuestionsParserRejectsUnknownHTML(t *testing.T) {
	for _, body := range []string{"<html>error</html>", questionBoot + questionBoot, strings.ReplaceAll(questionBoot, `"id":142`, `"id":136`), strings.ReplaceAll(questionBoot, `"question":"Question two?"`, `"question":""`)} {
		if _, err := parseSecurityQuestions([]byte(body)); err == nil {
			t.Fatal("invalid challenge accepted")
		}
	}
}

func TestMalformedSecurityQuestionDoesNotSubmitSavedAnswers(t *testing.T) {
	// Unmarshal can populate valid IDs/text while returning a type error for
	// number. Those partial fields are not a verified challenge.
	body := strings.ReplaceAll(questionBoot, `"number":2`, `"number":"invalid"`)
	p := &testQuestionProvider{answers: []SecurityQuestionAnswer{{ID: 136, Answer: "synthetic-one"}, {ID: 142, Answer: "synthetic-two"}}}
	req := func(_ context.Context, _ *hmeAuthState, method, _ string, _ []byte, _ http.Header) (*http.Response, []byte, error) {
		if method != http.MethodGet {
			t.Fatal("submitted answers to a partially decoded challenge")
		}
		return &http.Response{StatusCode: 412}, []byte(body), nil
	}
	if err := answerSecurityQuestions(context.Background(), "https://idmsa.apple.com/appleauth/auth", &hmeAuthState{}, req, func(*hmeAuthState) http.Header { return make(http.Header) }, p); err == nil {
		t.Fatal("partially decoded challenge accepted")
	}
}
