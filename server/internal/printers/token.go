package printers

import (
	"crypto/rand"
	"crypto/sha256"
)

const TokenPrefix = "bbr_printer_"

// GenerateToken returns a new random token and its hash.
func GenerateToken() (token string, hash []byte) {
	token = TokenPrefix + rand.Text()
	return token, HashToken(token)
}

// HashToken returns the SHA-256 hash of a token, as stored in printers.token_hash.
func HashToken(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}
