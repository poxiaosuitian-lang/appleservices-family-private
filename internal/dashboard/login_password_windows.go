//go:build windows

package dashboard

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	appleservices "github.com/Laky-64/appleservices"
)

type protectedLoginPassword struct {
	Version  int
	AppleID  string
	ADSID    string
	Password string
}

func loginPasswordEntropy(appleID, adsid string) []byte {
	hash := sha256.Sum256([]byte("appleservices-login-password-v1\x00" + strings.ToLower(strings.TrimSpace(appleID)) + "\x00" + adsid))
	return hash[:]
}

func protectLoginPasswordData(data, entropy []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 2<<20 || len(entropy) == 0 {
		return nil, errors.New("invalid protected login credential")
	}
	type dataBlob struct {
		Size uint32
		Data *byte
	}
	in := dataBlob{uint32(len(data)), &data[0]}
	extra := dataBlob{uint32(len(entropy)), &entropy[0]}
	var out dataBlob
	name := "CryptProtectData"
	if decrypt {
		name = "CryptUnprotectData"
	}
	proc := syscall.NewLazyDLL("crypt32.dll").NewProc(name)
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&in)), 0, uintptr(unsafe.Pointer(&extra)), 0, 0, 1, uintptr(unsafe.Pointer(&out)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if r == 0 {
		return nil, errors.New("Windows could not protect or restore login credential")
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Size > 2<<20 || out.Data == nil {
		return nil, errors.New("invalid protected login credential size")
	}
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}

func loadSavedLoginPassword(dir, appleID string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil || len(data) > 2<<20 {
		return "", errors.New("login credential binding is unavailable")
	}
	var saved appleservices.Session
	if json.Unmarshal(data, &saved) != nil || !strings.EqualFold(saved.AppleID, appleID) || saved.DSID == "" {
		return "", errors.New("login credential binding is incomplete")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "login-password.dpapi"))
	if err != nil {
		return "", err
	}
	var encrypted []byte
	if len(raw) > 32<<10 || json.Unmarshal(raw, &encrypted) != nil {
		return "", errors.New("invalid protected login credential")
	}
	plain, err := protectLoginPasswordData(encrypted, loginPasswordEntropy(appleID, saved.DSID), true)
	if err != nil {
		return "", err
	}
	defer clear(plain)
	var secret protectedLoginPassword
	if json.Unmarshal(plain, &secret) != nil || secret.Version != 1 || !strings.EqualFold(secret.AppleID, appleID) || secret.ADSID != saved.DSID || secret.Password == "" || len(secret.Password) > 4096 {
		return "", errors.New("protected login credential binding changed")
	}
	return secret.Password, nil
}

func hasSavedLoginPassword(dir, appleID string) bool {
	password, err := loadSavedLoginPassword(dir, appleID)
	return err == nil && password != ""
}
