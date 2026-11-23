//go:build !windows

package dashboard

import "errors"

func loadSavedLoginPassword(string, string) (string, error) {
	return "", errors.New("saved login credentials require Windows DPAPI")
}

func hasSavedLoginPassword(string, string) bool {
	return false
}
