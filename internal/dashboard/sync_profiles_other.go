//go:build !windows

package dashboard

import "errors"

func protectSyncProfiles([]byte, bool) ([]byte, error) {
	return nil, errors.New("保存同步密钥需要 Windows 本机加密支持")
}
