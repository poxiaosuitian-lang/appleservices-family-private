//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type dataBlob struct {
	Size uint32
	Data *byte
}

// DPAPI defaults to the current Windows user, never machine-wide scope.
func protectStoreData(data, entropy []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 2<<20 || len(entropy) == 0 {
		return nil, errors.New("invalid protected session")
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
		return nil, errors.New("Windows could not protect or restore the Store session")
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(uintptr(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Size > 2<<20 || out.Data == nil {
		return nil, errors.New("invalid protected session size")
	}
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}

func storeEntropy(appleID, dsid string) []byte {
	hash := sha256.Sum256([]byte("appleservices-store-v1\x00" + strings.ToLower(strings.TrimSpace(appleID)) + "\x00" + dsid))
	return hash[:]
}

type protectedStoreSession struct {
	Version int
	AppleID string
	DSID    string
	SavedAt time.Time
	State   []byte
}

func (s fileStore) SaveStoreSession(appleID, dsid string, state []byte) error {
	if appleID == "" || dsid == "" || len(state) == 0 || len(state) > 1<<20 {
		return errors.New("invalid Store checkpoint")
	}
	saved, err := s.LoadSession()
	if err != nil || saved == nil || !strings.EqualFold(saved.AppleID, appleID) {
		return errors.New("Store checkpoint account mismatch")
	}
	plain, err := json.Marshal(protectedStoreSession{1, appleID, dsid, time.Now().UTC(), state})
	if err != nil {
		return err
	}
	defer clear(plain)
	encrypted, err := protectStoreData(plain, storeEntropy(appleID, dsid), false)
	if err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(s.dir, "store-session.dpapi"), encrypted)
}

func (s fileStore) LoadStoreSession(appleID, dsid string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "store-session.dpapi"))
	if err != nil {
		return nil, err
	}
	var encrypted []byte
	if len(data) > 3<<20 || json.Unmarshal(data, &encrypted) != nil {
		return nil, errors.New("invalid Store checkpoint")
	}
	plain, err := protectStoreData(encrypted, storeEntropy(appleID, dsid), true)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var session protectedStoreSession
	if json.Unmarshal(plain, &session) != nil || session.Version != 1 || !strings.EqualFold(session.AppleID, appleID) || session.DSID != dsid ||
		time.Since(session.SavedAt) < 0 || time.Since(session.SavedAt) > 24*time.Hour {
		return nil, errors.New("saved Store session expired or binding changed")
	}
	return session.State, nil
}
