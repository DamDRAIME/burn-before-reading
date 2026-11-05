package printers

import (
	"fmt"
	"time"
)

// TimeOfDay is a wall-clock time with no date or zone. The printer's Timezone gives it meaning.
type TimeOfDay struct {
	Hour, Minute int
}

func ParseTimeOfDay(s string) (TimeOfDay, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return TimeOfDay{}, fmt.Errorf("invalid time of day %q, want HH:MM", s)
	}
	return TimeOfDay{Hour: t.Hour(), Minute: t.Minute()}, nil
}

func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

// encoding/json uses these automatically, so the JSON is "22:00".
func (t TimeOfDay) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

func (t *TimeOfDay) UnmarshalText(b []byte) error {
	v, err := ParseTimeOfDay(string(b))
	if err != nil {
		return err
	}
	*t = v
	return nil
}
