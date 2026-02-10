package appspec

import (
	"fmt"
)

// Mode represents a parsed octal file mode from the appspec permissions section.
type Mode struct {
	// Raw is the octal string representation (e.g. "0755").
	Raw string
	// Value is the numeric file mode (e.g. 0755 → 493 decimal).
	Value uint32
}

// ParseMode parses an octal mode value from an appspec permission entry.
// The input may be an integer or string. Valid modes are 1-4 octal digits.
//
//	mode, err := appspec.ParseMode("0755")
//	// mode.Value == 0755, mode.Raw == "0755"
func ParseMode(v interface{}) (Mode, error) {
	s := fmt.Sprintf("%v", v)

	// Pad to at least 3 chars
	for len(s) < 3 {
		s = "0" + s
	}

	if len(s) > 4 {
		return Mode{}, fmt.Errorf("appspec: mode %q must be 1-4 octal digits", s)
	}

	var val uint32
	for _, ch := range s {
		if ch < '0' || ch > '7' {
			return Mode{}, fmt.Errorf("appspec: mode %q contains non-octal character %c", s, ch)
		}
		val = val*8 + uint32(ch-'0')
	}

	return Mode{Raw: s, Value: val}, nil
}
