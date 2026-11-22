package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	appleservices "github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
	"github.com/Laky-64/appleservices/nativeadi"
	"github.com/google/uuid"
	"howett.net/plist"
)

// A saved GSA account and its provisioned device are sufficient for local
// generation. iCloud delegate acceptance is an independent service session.
func localDeviceConfig(account *Account) (nativeadi.Config, error) {
	var config nativeadi.Config
	if account == nil {
		return config, errors.New("账号不存在")
	}
	var session appleservices.Session
	data, err := os.ReadFile(filepath.Join(account.Dir, "session.json"))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &session) != nil ||
		!strings.EqualFold(session.AppleID, account.AppleID) || session.DSID == "" || session.DeviceIdentityToken == "" {
		return config, errors.New("尚无与该账号绑定的设备会话")
	}
	var device appleservices.Device
	data, err = os.ReadFile(filepath.Join(account.Dir, "device.json"))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &device) != nil || len(device.Identifier) != 16 || device.SerialNumber == "" {
		return config, errors.New("账号设备身份不可用")
	}
	profile := filepath.Join(account.Dir, "native-adi")
	var hardware struct {
		OSConfig struct {
			DeviceID string `plist:"device_id"`
			Inner    struct {
				Serial string `plist:"platform_serial_number"`
			} `plist:"inner"`
		} `plist:"os_config"`
		Push struct {
			Token []byte `plist:"token"`
		} `plist:"push"`
	}
	data, err = os.ReadFile(filepath.Join(profile, "hw_info.plist"))
	if err != nil || len(data) > 2<<20 {
		return config, errors.New("账号本机 Mac 身份不可用")
	}
	if _, err := plist.Unmarshal(data, &hardware); err != nil ||
		!strings.EqualFold(hardware.OSConfig.DeviceID, uuid.UUID(device.Identifier).String()) ||
		hardware.OSConfig.Inner.Serial != device.SerialNumber || len(hardware.Push.Token) != 32 {
		return config, errors.New("本机 Mac 身份与该账号的设备不一致")
	}
	var provisioned struct {
		Identifier []byte `plist:"keychain_identifier"`
	}
	data, err = os.ReadFile(filepath.Join(profile, "anisette_test", "state.plist"))
	if err != nil || len(data) > 2<<20 {
		return config, errors.New("该账号尚无本机设备配置")
	}
	if _, err := plist.Unmarshal(data, &provisioned); err != nil || !bytes.Equal(provisioned.Identifier, device.Identifier) {
		return config, errors.New("本机设备配置与账号不一致")
	}
	config.ProfileDir, config.Identifier = profile, device.Identifier
	config.Serial, config.PushToken = device.SerialNumber, hardware.Push.Token
	return config, nil
}

func generateLocalDeviceCode(ctx context.Context, config nativeadi.Config) (string, error) {
	client, err := nativeadi.NewClient(config)
	if err != nil {
		return "", err
	}
	defer client.Close()
	if err := client.Start(ctx); err != nil {
		return "", err
	}
	if _, err := client.Headers(); err != nil {
		return "", err
	}
	return client.Code(ctx)
}

func (s *Server) localDeviceCodeAction(parent context.Context, account *Account) control.Result {
	if !s.launchMu.TryLock() {
		return control.Result{Message: "正在处理本机设备，请稍后重试"}
	}
	defer s.launchMu.Unlock()
	if account != nil && s.readStatusFrom(account.Dir).Running {
		return control.Result{Message: "请先停止该账号后台，再取本机验证码"}
	}
	config, err := localDeviceConfig(account)
	if err != nil {
		return control.Result{Message: err.Error()}
	}
	config.LibraryPath, config.Python = s.config.Library, s.config.Python
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	code, err := s.generateLocalCode(ctx, config)
	if err != nil || len(code) != 6 || strings.IndexFunc(code, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return control.Result{Message: "本机取码失败，请检查该账号的设备配置"}
	}
	return control.Result{OK: true, AccountID: account.ID, Code: code, CodeKind: "local-device", HideAt: time.Now().Add(time.Minute)}
}
