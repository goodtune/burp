package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func signature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
