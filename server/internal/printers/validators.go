package printers

import (
	"strings"
	"time"
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
