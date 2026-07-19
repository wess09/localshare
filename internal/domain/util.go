package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

func SHA256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func SockName(username string) string {
	digest := SHA256Text(username)
	if len(digest) < SockNameLength {
		return digest
	}
	return digest[:SockNameLength]
}

func IsHashToken(value string) bool {
	if len(value) < 8 {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func ParseSSHArguments(command string) map[string]string {
	out := map[string]string{}
	parts := strings.Fields(command)
	for i, part := range parts {
		if part == "--output" && i+1 < len(parts) {
			out["output"] = parts[i+1]
		} else if strings.HasPrefix(part, "--output=") {
			out["output"] = strings.TrimPrefix(part, "--output=")
		}
	}
	return out
}
