package main

import (
	"strings"
	"time"
)

const appPasswordFile = "app-password.dpapi"

type appPasswordRecord struct {
	Password  string
	Label     string
	ID        string
	CreatedAt time.Time
}

func validAppPassword(password string) bool {
	if len(password) != 19 {
		return false
	}
	for i := range password {
		if i == 4 || i == 9 || i == 14 {
			if password[i] != '-' {
				return false
			}
		} else if password[i] < 'a' || password[i] > 'z' {
			return false
		}
	}
	return true
}

func validAppPasswordRecord(record *appPasswordRecord) bool {
	return record != nil && validAppPassword(record.Password) &&
		strings.TrimSpace(record.Label) != "" && len(record.Label) <= 1024 &&
		strings.TrimSpace(record.ID) != "" && len(record.ID) <= 1024 && !record.CreatedAt.IsZero()
}
