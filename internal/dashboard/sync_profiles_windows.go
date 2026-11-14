//go:build windows

package dashboard

import (
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

// Current-user DPAPI, with a separate purpose from account/session credentials.
func protectSyncProfiles(data []byte, decrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > 128<<10 {
		return nil, errors.New("服务器配置大小无效")
	}
	type blob struct {
		Size uint32
		Data *byte
	}
	entropy := []byte("appleservices-sync-profiles-v1")
	input := blob{uint32(len(data)), &data[0]}
	extra := blob{uint32(len(entropy)), &entropy[0]}
	var output blob
	name := "CryptProtectData"
	if decrypt {
		name = "CryptUnprotectData"
	}
	result, _, _ := syscall.NewLazyDLL("crypt32.dll").NewProc(name).Call(uintptr(unsafe.Pointer(&input)), 0, uintptr(unsafe.Pointer(&extra)), 0, 0, 1, uintptr(unsafe.Pointer(&output)))
	runtime.KeepAlive(data)
	runtime.KeepAlive(entropy)
	if result == 0 {
		return nil, errors.New("无法使用当前 Windows 用户加密或解密服务器配置")
	}
	defer syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree").Call(uintptr(unsafe.Pointer(output.Data)))
	if output.Size == 0 || output.Size > 128<<10 || output.Data == nil {
		return nil, errors.New("服务器配置解密失败")
	}
	return append([]byte(nil), unsafe.Slice(output.Data, int(output.Size))...), nil
}
