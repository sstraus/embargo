package policy

import (
	"fmt"
	"strings"
	"time"
)

// HumanDuration renders a duration using the two most-significant non-zero
// units among days, hours, and minutes (e.g. "3d", "3d 3h", "3h 21m", "45m").
// Sub-minute durations render as "<1m". Negative durations are clamped to 0.
func HumanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		if d == 0 {
			return "0m"
		}
		return "<1m"
	}

	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	mins := int((d % time.Hour) / time.Minute)

	parts := make([]string, 0, 2)
	add := func(v int, unit string) {
		if len(parts) < 2 && v > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", v, unit))
		}
	}
	add(days, "d")
	add(hours, "h")
	add(mins, "m")

	if len(parts) == 0 {
		return "0m"
	}
	return strings.Join(parts, " ")
}
