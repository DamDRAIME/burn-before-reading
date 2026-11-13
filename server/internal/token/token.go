package token

import (
	"crypto/rand"
	"crypto/sha256"
)

type Prefix string

const (
	PrinterPrefix Prefix = "bbr_printer_"
	InvitePrefix  Prefix = "bbr_invite_"
	SessionPrefix Prefix = "bbr_user_"
)

// Generate returns a new random token and its hash.
func Generate(prefix Prefix) (token string, hash []byte) {
	token = string(prefix) + rand.Text()
	return token, Hash(token)
}

// Hash returns the SHA-256 hash of a token, as stored in printers.token_hash.
func Hash(t string) []byte {
	hash := sha256.Sum256([]byte(t))
	return hash[:]
}
