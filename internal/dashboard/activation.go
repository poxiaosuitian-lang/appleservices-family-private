package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	appleservices "github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
)

func verifiedGSACheckpointReady(dir, appleID string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil || len(data) > 2<<20 {
		return false
	}
	var session appleservices.Session
	if json.Unmarshal(data, &session) != nil || !strings.EqualFold(session.AppleID, appleID) || session.DSID == "" {
		return false
	}
	return session.Tokens["com.apple.gs.idms.pet"] != ""
}

func activationTermsURLs(region string) (string, string) {
	icloudURL := "https://www.apple.com/legal/internet-services/icloud/"
	mediaURL := "https://www.apple.com/legal/internet-services/itunes/us/terms.html"
	if !strings.EqualFold(region, "US") {
		mediaURL = "https://www.apple.com/legal/internet-services/itunes/"
	}
	return icloudURL, mediaURL
}

func parseActivationPlan(output []byte) (*control.ActivationInfo, error) {
	for _, line := range strings.Split(string(output), "\n") {
		var value struct {
			TermsPlan     bool   `json:"terms-plan"`
			Region        string `json:"region"`
			ICloudVersion string `json:"icloud-version"`
			MediaVersion  string `json:"media-version"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &value) != nil || !value.TermsPlan {
			continue
		}
		if value.Region == "" || value.ICloudVersion == "" {
			return nil, errors.New("Apple 未返回完整条款版本")
		}
		icloudURL, mediaURL := activationTermsURLs(value.Region)
		return &control.ActivationInfo{Required: true, Region: value.Region, ICloudVersion: value.ICloudVersion, MediaVersion: value.MediaVersion, ICloudTermsURL: icloudURL, MediaTermsURL: mediaURL}, nil
	}
	return nil, errors.New("Apple 未返回可确认的 iCloud 条款计划")
}

func activationTuple(info *control.ActivationInfo) string {
	if info == nil {
		return ""
	}
	return info.Region + ":" + info.ICloudVersion + ":" + info.MediaVersion
}

func filteredActivationEnv(appleID string) []string {
	result := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "APPLESERVICES_APPLEID"), strings.EqualFold(key, "APPLESERVICES_PASSWORD"),
			strings.EqualFold(key, "HTTP_PROXY"), strings.EqualFold(key, "HTTPS_PROXY"), strings.EqualFold(key, "ALL_PROXY"),
			strings.EqualFold(key, "APPLESERVICES_SOCKS_PROXY"), strings.EqualFold(key, "APPLESERVICES_DELEGATE_DUMP"),
			strings.EqualFold(key, "APPLESERVICES_DELEGATE_PATH"), strings.EqualFold(key, "__COMPAT_LAYER"):
			continue
		}
		result = append(result, entry)
	}
	// accountsetupcheck.exe contains "setup" in its filename, which triggers
	// Windows UAC Installer Detection (ERROR_ELEVATION_REQUIRED) without an
	// embedded manifest unless RunAsInvoker is requested.
	return append(result, "APPLESERVICES_APPLEID="+appleID, "APPLESERVICES_DELEGATE_PATH=v2", "NO_PROXY=*", "__COMPAT_LAYER=RunAsInvoker")
}

func runActivationCommand(ctx context.Context, root, appleID, executable string, args ...string) ([]byte, error) {
	return runActivationCommandWithPassword(ctx, root, appleID, "", executable, args...)
}

func runActivationCommandWithPassword(ctx context.Context, root, appleID, password, executable string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	configureProcess(command)
	command.Dir = root
	env := filteredActivationEnv(appleID)
	if password != "" {
		env = append(env, "APPLESERVICES_PASSWORD="+password)
	}
	command.Env = env
	output, err := command.CombinedOutput()
	if len(output) > 256<<10 {
		return nil, errors.New("激活组件返回内容过大")
	}
	return output, err
}

func copyActivationTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || strings.HasPrefix(relative, "..") || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("激活运行时路径无效")
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		return errors.Join(copyErr, closeErr)
	})
}

func (s *Server) activationRoot() string {
	return filepath.Dir(filepath.Dir(s.config.MacPushSetup))
}

func isActivationUnauthorized(output []byte) bool {
	return strings.Contains(string(output), `"HTTP":401`)
}

func isActivationTermsUpdateRequired(output []byte) bool {
	return strings.Contains(string(output), "MOBILEME_TERMS_OF_SERVICE_UPDATE")
}

func isActivationDelegateUnauthorized(output []byte) bool {
	return strings.Contains(string(output), "HTTP 401") || strings.Contains(string(output), "ErrorID=UNAUTHORIZED")
}

func activationDelegateSucceeded(output []byte, err error) bool {
	return err == nil &&
		strings.Contains(string(output), "delegate accepted; mme-token-present=true") &&
		strings.Contains(string(output), "quota accepted;")
}

var activationDetailPatterns = []*regexp.Regexp{
	regexp.MustCompile(`HTTP\s+\d{3}`),
	regexp.MustCompile(`ErrorID=[A-Za-z0-9_.-]+`),
	regexp.MustCompile(`reason-code=[0-9A-Za-z_-]+`),
	regexp.MustCompile(`status=[0-9-]+`),
	regexp.MustCompile(`Account limit reached\.?`),
}

func safeActivationDetail(output []byte) string {
	var parts []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if trimmed == "" || strings.Contains(trimmed, "@") || strings.Contains(lower, "password") ||
			strings.Contains(lower, "secret") || strings.Contains(lower, "cookie") || strings.Contains(lower, "dsid") ||
			(strings.Contains(lower, "token") && !strings.Contains(lower, "mme-token-present=")) {
			continue
		}
		for _, re := range activationDetailPatterns {
			for _, match := range re.FindAllString(trimmed, -1) {
				if !seen[match] {
					seen[match] = true
					parts = append(parts, match)
				}
			}
		}
	}
	detail := strings.Join(parts, ", ")
	if len(detail) > 120 {
		detail = detail[:120]
	}
	return detail
}

func replaceGSACheckpoint(source, destination, appleID string) error {
	data, err := os.ReadFile(source)
	if err != nil || len(data) == 0 || len(data) > 2<<20 {
		return errors.New("Apple 验证会话不可用")
	}
	var session appleservices.Session
	if json.Unmarshal(data, &session) != nil || !strings.EqualFold(session.AppleID, appleID) || session.DSID == "" || session.Tokens["com.apple.gs.idms.pet"] == "" {
		return errors.New("Apple 验证会话与当前账号不匹配")
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".session-*.tmp")
	if err != nil {
		return err
	}
	cleanup := file.Name()
	defer os.Remove(cleanup)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(cleanup, destination)
}

func (s *Server) activationPlan(ctx context.Context, account *Account, runDir, password string) (*control.ActivationInfo, string, bool, error) {
	root := s.activationRoot()
	helper := filepath.Join(filepath.Dir(s.config.Worker), "accountsetupcheck.exe")
	resumePath := filepath.Join(account.Dir, "session.json")
	dump := filepath.Join(runDir, "setup.html")
	output, err := runActivationCommand(ctx, root, account.AppleID, helper,
		"-dir", account.Dir, "-resume", resumePath, "-dump", dump)
	if err != nil {
		if isActivationUnauthorized(output) {
			if password != "" {
				refreshed := filepath.Join(runDir, "gsa-refreshed.json")
				refreshDump := filepath.Join(runDir, "setup-refreshed.html")
				refreshedOut, refreshErr := runActivationCommandWithPassword(ctx, root, account.AppleID, password, helper,
					"-dir", account.Dir, "-checkpoint", refreshed, "-dump", refreshDump)
				if refreshErr == nil {
					plan, planErr := parseActivationPlan(refreshedOut)
					if planErr == nil {
						_ = replaceGSACheckpoint(refreshed, resumePath, account.AppleID)
						return plan, refreshed, true, nil
					}
					return nil, "", false, planErr
				}
				if isActivationUnauthorized(refreshedOut) && replaceGSACheckpoint(refreshed, resumePath, account.AppleID) == nil {
					return nil, refreshed, false, nil
				}
			}
			if info, statErr := os.Stat(resumePath); statErr == nil && time.Since(info.ModTime()) < 2*time.Minute {
				return nil, resumePath, false, nil
			}
			return nil, "", false, errors.New("Apple 登录凭证已过期，请点击“重新连接”重新验证后再激活")
		}
		return nil, "", false, errors.New("无法读取 Apple 当前条款状态")
	}
	plan, err := parseActivationPlan(output)
	return plan, resumePath, err == nil, err
}

func readTermsReceipt(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return false
	}
	var receipt struct {
		Success bool `json:"success"`
	}
	return json.Unmarshal(data, &receipt) == nil && receipt.Success
}

func installAcceptedCheckpoint(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil || len(data) == 0 || len(data) > 2<<20 {
		return errors.New("Apple 验收会话不可用")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func shouldAutoActivateICloud(diagnosticPath string, account *Account) bool {
	if account == nil || acceptedCheckpointReady(account.Dir, account.AppleID) || !verifiedGSACheckpointReady(account.Dir, account.AppleID) {
		return false
	}
	data, err := os.ReadFile(diagnosticPath)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		return strings.Contains(line, "mobileme delegate refused status=1")
	}
	return false
}

func (s *Server) autoActivateAndRestart(account *Account, creds credentials) {
	defer func() { creds.Password = "" }()
	if account == nil || creds.Password == "" {
		return
	}
	s.setLaunching(account, true)
	s.setMessage(account, "检测到账号尚未激活 iCloud，正在自动同意条款并完成首次激活…")
	result := s.runActivation(context.Background(), account, true, "", true, creds.Password)
	if !result.OK {
		s.setLaunching(account, false)
		s.setMessage(account, "自动激活 iCloud 未完成（"+result.Message+"）；请在下方“激活 iCloud”卡片重试")
		return
	}
	cacheSafeActionResult(account, &result)
	s.setMessage(account, "iCloud 已自动激活，正在重新连接后台…")
	s.setLaunching(account, false)
	restarted := s.startInModeInternal(context.Background(), account.Dir, creds, account, true, false)
	if !restarted.OK {
		s.setMessage(account, "iCloud 已自动激活，但重新连接未完成："+restarted.Message)
	}
}

func (s *Server) acceptActivationTerms(ctx context.Context, root string, account *Account, resumePath, approvedTuple, workDir string) bool {
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return false
	}
	helper := filepath.Join(filepath.Dir(s.config.Worker), "accountsetupcheck.exe")
	termsDump := filepath.Join(workDir, "terms.html")
	_, _ = runActivationCommand(ctx, root, account.AppleID, helper,
		"-dir", account.Dir, "-resume", resumePath, "-accept-terms", "-expected-terms", approvedTuple, "-dump", termsDump)
	return readTermsReceipt(termsDump + ".update.json")
}

func (s *Server) generateActivationNAC(ctx context.Context, root string, account *Account, workDir string) (string, error) {
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return "", err
	}
	runtimeSource := filepath.Join(root, ".cache", "mac-nac-runtime")
	runtimeCopy := filepath.Join(workDir, "nac-runtime")
	if err := copyActivationTree(runtimeSource, runtimeCopy); err != nil {
		return "", errors.New("无法准备 Mac NAC 运行时")
	}
	validation := filepath.Join(workDir, "nac-validation.bin")
	nacOutput, nacErr := runActivationCommand(ctx, root, account.AppleID, s.config.Python,
		filepath.Join(root, "tools", "mac-nac-check.py"), "--profile", filepath.Join(account.Dir, "native-adi", "hw_info.plist"),
		"--runtime", runtimeCopy, "--encrypt-dll", filepath.Join(root, ".cache", "activation-source-review", "mac-io-encrypt.dll"),
		"--validation-out", validation, "--synthetic-missing-board-fields", "--transport", filepath.Join(filepath.Dir(s.config.Worker), "nacexchange.exe"))
	if nacErr != nil || !strings.Contains(string(nacOutput), `"stage": "NACSign"`) {
		return "", errors.New("Mac NAC 生成未完成")
	}
	return validation, nil
}

func (s *Server) submitActivationDelegate(ctx context.Context, root string, account *Account, resumePath, validationPath, workDir string, termsAccepted bool, password string) (string, []byte, error) {
	if err := os.MkdirAll(workDir, 0700); err != nil {
		return "", nil, err
	}
	helper := filepath.Join(filepath.Dir(s.config.Worker), "accountsetupcheck.exe")
	buildArgs := func(authArgs []string, dumpName, outCheckpoint string) []string {
		args := append([]string{"-dir", account.Dir}, authArgs...)
		args = append(args, "-delegate-only")
		if termsAccepted {
			args = append(args, "-terms-accepted")
		}
		return append(args, "-nac-validation", validationPath, "-dump", filepath.Join(workDir, dumpName), "-icloud-checkpoint", outCheckpoint)
	}
	checkpoint := filepath.Join(workDir, "icloud-accepted.json")
	output, err := runActivationCommand(ctx, root, account.AppleID, helper, buildArgs([]string{"-resume", resumePath}, "delegate.plist", checkpoint)...)
	if !activationDelegateSucceeded(output, err) && isActivationDelegateUnauthorized(output) && password != "" {
		refreshedGSA := filepath.Join(workDir, "gsa-delegate-refreshed.json")
		refreshedCheckpoint := filepath.Join(workDir, "icloud-accepted-refreshed.json")
		retryOutput, retryErr := runActivationCommandWithPassword(ctx, root, account.AppleID, password, helper,
			buildArgs([]string{"-checkpoint", refreshedGSA}, "delegate-refreshed.plist", refreshedCheckpoint)...)
		if activationDelegateSucceeded(retryOutput, retryErr) || isActivationTermsUpdateRequired(retryOutput) {
			_ = replaceGSACheckpoint(refreshedGSA, filepath.Join(account.Dir, "session.json"), account.AppleID)
		}
		return refreshedCheckpoint, retryOutput, retryErr
	}
	return checkpoint, output, err
}

func (s *Server) activationAction(parent context.Context, account *Account, accept bool, approvedTuple string) control.Result {
	password := ""
	if account != nil {
		password, _ = loadSavedLoginPassword(account.Dir, account.AppleID)
	}
	return s.runActivation(parent, account, accept, approvedTuple, false, password)
}

func (s *Server) runActivationAttempt(ctx context.Context, root, runDir, suffix string, account *Account, plan *control.ActivationInfo, resumePath string, termsRequired, accept, autoApprove bool, approvedTuple, password string) (string, []byte, error, *control.Result) {
	if termsRequired {
		if !accept {
			res := control.Result{OK: true, Message: "Apple 要求接受当前 iCloud 与媒体服务条款", Stage: "activation-terms", Activation: plan}
			return "", nil, nil, &res
		}
		if autoApprove {
			approvedTuple = activationTuple(plan)
		}
		if approvedTuple == "" || approvedTuple != activationTuple(plan) {
			res := control.Result{Message: "Apple 条款版本已变化，请重新查看并确认", Stage: "activation-consent", Activation: plan}
			return "", nil, nil, &res
		}
		if !s.acceptActivationTerms(ctx, root, account, resumePath, approvedTuple, filepath.Join(runDir, "terms-"+suffix)) {
			res := control.Result{Message: "Apple 未确认条款接受状态", Stage: "activation-terms", Activation: plan}
			return "", nil, nil, &res
		}
	}
	validation, err := s.generateActivationNAC(ctx, root, account, filepath.Join(runDir, "nac-"+suffix))
	if err != nil {
		res := control.Result{Message: err.Error(), Stage: "activation-nac"}
		return "", nil, nil, &res
	}
	checkpoint, output, delegateErr := s.submitActivationDelegate(ctx, root, account, resumePath, validation, filepath.Join(runDir, "delegate-"+suffix), termsRequired, password)
	return checkpoint, output, delegateErr, nil
}

func (s *Server) runActivation(parent context.Context, account *Account, accept bool, approvedTuple string, autoApprove bool, password string) control.Result {
	if account == nil || !verifiedGSACheckpointReady(account.Dir, account.AppleID) {
		return control.Result{Message: "请先完成账号密码与验证码验证", Stage: "activation-session"}
	}
	if acceptedCheckpointReady(account.Dir, account.AppleID) {
		result := s.acceptedStorageAction(parent, account, false)
		result.Activation = &control.ActivationInfo{Completed: result.OK}
		return result
	}
	if !s.launchMu.TryLock() {
		return control.Result{Message: "当前账号正在处理其他登录或激活操作", Stage: "activation-busy"}
	}
	defer s.launchMu.Unlock()
	root := s.activationRoot()
	cacheRoot := filepath.Join(root, ".cache")
	runDir, err := os.MkdirTemp(cacheRoot, "dashboard-activation-")
	if err != nil {
		return control.Result{Message: "无法创建私有激活工作区", Stage: "activation-local"}
	}
	defer os.RemoveAll(runDir)
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	plan, resumePath, termsRequired, err := s.activationPlan(ctx, account, runDir, password)
	if err != nil {
		return control.Result{Message: err.Error(), Stage: "activation-plan"}
	}
	checkpoint, delegateOutput, delegateErr, early := s.runActivationAttempt(ctx, root, runDir, "1", account, plan, resumePath, termsRequired, accept, autoApprove, approvedTuple, password)
	if early != nil {
		return *early
	}
	if !activationDelegateSucceeded(delegateOutput, delegateErr) && !termsRequired && isActivationTermsUpdateRequired(delegateOutput) {
		termsRetryDir := filepath.Join(runDir, "terms-retry")
		if err := os.MkdirAll(termsRetryDir, 0700); err == nil {
			retryPlan, retryResumePath, retryTermsRequired, retryErr := s.activationPlan(ctx, account, termsRetryDir, password)
			if retryErr == nil && retryTermsRequired && retryPlan != nil {
				plan, resumePath = retryPlan, retryResumePath
				checkpoint, delegateOutput, delegateErr, early = s.runActivationAttempt(ctx, root, runDir, "2", account, plan, resumePath, true, accept, autoApprove, approvedTuple, password)
				if early != nil {
					return *early
				}
			}
		}
	}
	if !activationDelegateSucceeded(delegateOutput, delegateErr) {
		message := "Apple 尚未接受 Mac v2/NAC 激活"
		if detail := safeActivationDetail(delegateOutput); detail != "" {
			message += "（" + detail + "）"
		}
		return control.Result{Message: message, Stage: "activation-delegate"}
	}
	destination := filepath.Join(account.Dir, "icloud-accepted.json")
	if err := installAcceptedCheckpoint(checkpoint, destination); err != nil && !acceptedCheckpointReady(account.Dir, account.AppleID) {
		return control.Result{Message: "激活成功，但本地验收状态未保存", Stage: "activation-save"}
	}
	result := s.acceptedStorageAction(ctx, account, false)
	if !result.OK {
		return result
	}
	result.Stage = "activation-completed"
	result.Message = "iCloud 条款、Mac NAC、delegate 与配额验收均已完成"
	if plan != nil {
		result.Activation = &control.ActivationInfo{Completed: true, Region: plan.Region, ICloudVersion: plan.ICloudVersion, MediaVersion: plan.MediaVersion, ICloudTermsURL: plan.ICloudTermsURL, MediaTermsURL: plan.MediaTermsURL}
	} else {
		result.Activation = &control.ActivationInfo{Completed: true}
	}
	return result
}
