package validate

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidCharacters = errors.New("text contains control characters")

// RequiredString trims s and checks it's non-empty, has no control chars, and fits maxLen runes.
func RequiredString(s string, maxLen int, errEmpty, errTooLong error, allowNewlines bool) (string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", errEmpty
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool {
		return unicode.IsControl(r) && !(allowNewlines && (r == '\n' || r == '\r'))
	}) {
		return "", ErrInvalidCharacters
	}
	if utf8.RuneCountInString(trimmed) > maxLen {
		return "", errTooLong
	}
	return trimmed, nil
}

// OptionalString is RequiredString for PATCH-style fields; nil means "not provided".
func OptionalString(s *string, maxLen int, errEmpty, errTooLong error, allowNewlines bool) (*string, error) {
	if s == nil {
		return nil, nil
	}
	v, err := RequiredString(*s, maxLen, errEmpty, errTooLong, allowNewlines)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
