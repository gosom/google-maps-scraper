package utils

import (
	"crypto/rand"
	"encoding/base64"
)

func GenerateGUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}

	// Convert to a URL-safe base64 string
	s := base64.URLEncoding.EncodeToString(b)
	return s
}
