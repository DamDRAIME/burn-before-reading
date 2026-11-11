package messages

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func sanitizeString(s string, allowNewLines bool) string {
	sanitized := strings.ReplaceAll(s, "\r\n", "\n")
	sanitized = strings.ReplaceAll(sanitized, "\r", "\n")

	var builder strings.Builder
	for _, r := range sanitized {
		if r == '\n' {
			if !allowNewLines {
				builder.WriteRune(' ')
				continue
			}
			builder.WriteRune(r)
			continue
		} else if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
