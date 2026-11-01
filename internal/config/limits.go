package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/store"
)

// limitsFile is config.json's optional "limits" object. A size is a JSON integer or a string such as "16MiB"; an age
// is a string such as "30d" or "720h".
type limitsFile struct {
	InboxMaxBytes     json.RawMessage `json:"inbox_max_bytes"`
	InboxMaxAge       json.RawMessage `json:"inbox_max_age"`
	DirectoryMaxBytes json.RawMessage `json:"directory_max_bytes"`
	LeaseMaxBytes     json.RawMessage `json:"lease_max_bytes"`
	InboxMaxConsumers *int            `json:"inbox_max_consumers"`
}

// ParseLimits applies a "limits" object to the defaults. An empty raw is the defaults. Unknown keys and invalid
// values are errors naming the key.
func ParseLimits(raw json.RawMessage) (store.Limits, error) {
	l := store.DefaultLimits()
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return l, nil
	}
	var f limitsFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return l, fmt.Errorf("mash: config.json \"limits\": %w", err)
	}
	for _, p := range []struct {
		key string
		src json.RawMessage
		dst *int64
	}{{"inbox_max_bytes", f.InboxMaxBytes, &l.InboxMaxBytes}, {"directory_max_bytes", f.DirectoryMaxBytes, &l.DirectoryMaxBytes},
		{"lease_max_bytes", f.LeaseMaxBytes, &l.LeaseMaxBytes}} {
		if len(p.src) == 0 {
			continue
		}
		n, err := parseSizeJSON(p.src)
		if err != nil {
			return l, fmt.Errorf("mash: config.json limits %q: %w", p.key, err)
		}
		*p.dst = n
	}
	if len(f.InboxMaxAge) > 0 {
		var s string
		if err := json.Unmarshal(f.InboxMaxAge, &s); err != nil {
			return l, fmt.Errorf("mash: config.json limits \"inbox_max_age\": want a string such as \"30d\" or \"720h\"")
		}
		d, err := ParseAge(s)
		if err != nil {
			return l, fmt.Errorf("mash: config.json limits \"inbox_max_age\": %w", err)
		}
		l.InboxMaxAge = d
	}
	if f.InboxMaxConsumers != nil {
		l.InboxMaxConsumers = *f.InboxMaxConsumers
	}
	if err := l.Validate(); err != nil {
		return l, fmt.Errorf("mash: config.json: %w", err)
	}
	return l, nil
}

func parseSizeJSON(raw json.RawMessage) (int64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return ParseSize(s)
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("want an integer or a string such as \"16MiB\"")
	}
	if n <= 0 {
		return 0, fmt.Errorf("%d is not above zero", n)
	}
	return n, nil
}

// ParseSize parses a byte size: a plain integer, or an integer with a B, KiB, MiB or GiB suffix.
func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"B", 1}} {
		if strings.HasSuffix(t, u.suffix) {
			t, mult = strings.TrimSpace(strings.TrimSuffix(t, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n <= 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("%q is not a size above zero (for example \"16MiB\", \"512KiB\" or 1048576)", s)
	}
	return n * mult, nil
}

// ParseAge parses an age: time.ParseDuration, or a whole number of days with a d suffix ("30d").
func ParseAge(s string) (time.Duration, error) {
	if t, ok := strings.CutSuffix(strings.TrimSpace(s), "d"); ok {
		n, err := strconv.ParseInt(t, 10, 32)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%q is not an age above zero (for example \"30d\" or \"720h\")", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%q is not an age above zero (for example \"30d\" or \"720h\")", s)
	}
	return d, nil
}

// FormatSize renders n with the largest of GiB, MiB, KiB that divides it exactly, else as plain bytes.
func FormatSize(n int64) string {
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n >= u.mult && n%u.mult == 0 {
			return strconv.FormatInt(n/u.mult, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}

// FormatAge renders d as whole days when it is one, else as a Go duration.
func FormatAge(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
	return d.String()
}
