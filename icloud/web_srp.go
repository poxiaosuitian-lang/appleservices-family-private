package icloud

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// The protocol is chosen by Apple, not by account age or the caller.
func deriveWebPasswordKey(password, protocol string, salt []byte, iterations int) ([]byte, error) {
	if iterations <= 0 || iterations > 10000000 || len(salt) == 0 {
		return nil, errors.New("Apple web SRP parameters are invalid")
	}
	hash := sha256.Sum256([]byte(password))
	var input string
	switch protocol {
	case "s2k":
		input = string(hash[:])
	case "s2k_fo":
		input = hex.EncodeToString(hash[:])
	default:
		return nil, errors.New("Apple web SRP protocol is unsupported")
	}
	return pbkdf2.Key(sha256.New, input, salt, iterations, 32)
}
