package printers

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func normalizeTimezone(timezone *string) (string, bool) {
	if timezone == nil {
		return DefaultTimezone, true
	}
	timezoneStr := strings.TrimSpace(*timezone)
	if timezoneStr == "" || timezoneStr == "Local" {
		return "", false
	}
	_, err := time.LoadLocation(timezoneStr)
	if err != nil {
		return "", false
	}
	return timezoneStr, true
}

func validateOptionalString(s *string, maxLength int, errEmpty, errTooLong error) (*string, error) {
	if s == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*s)
	if trimmed == "" {
		return nil, errEmpty
	}
	if strings.ContainsFunc(trimmed, unicode.IsControl) {
		return nil, ErrInvalidCharacters
	}
	if utf8.RuneCountInString(trimmed) > maxLength {
		return nil, errTooLong
	}
	return &trimmed, nil
}
