package model

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// TimeLayout is the only accepted timestamp layout: RFC3339 with an explicit
// UTC designator and whole seconds. Restricting the layout keeps every stored
// artifact byte-identical for identical inputs.
const TimeLayout = "2006-01-02T15:04:05Z"

// DateLayout is the calendar-day key layout used for per-day groupings.
const DateLayout = "2006-01-02"

// UTCTime is an immutable second-resolution UTC instant.
type UTCTime struct {
	t time.Time
}

// NewUTC normalizes an arbitrary time to a second-resolution UTC instant.
func NewUTC(t time.Time) UTCTime {
	return UTCTime{t: t.UTC().Truncate(time.Second)}
}

// ParseUTC parses a timestamp, rejecting anything that is not exactly
// TimeLayout (no offsets, no fractional seconds, no local times).
func ParseUTC(s string) (UTCTime, error) {
	if s == "" {
		return UTCTime{}, fmt.Errorf("empty timestamp")
	}
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		return UTCTime{}, fmt.Errorf("timestamp %q must match %s: %w", s, TimeLayout, err)
	}
	// time.Parse tolerates a fractional second that the layout does not mention,
	// so require an exact round trip to keep the accepted set narrow.
	if t.UTC().Format(TimeLayout) != s {
		return UTCTime{}, fmt.Errorf("timestamp %q must match %s exactly", s, TimeLayout)
	}
	return UTCTime{t: t.UTC()}, nil
}

// MustParseUTC is ParseUTC for statically known constants.
func MustParseUTC(s string) UTCTime {
	u, err := ParseUTC(s)
	if err != nil {
		panic(err)
	}
	return u
}

// Time returns the underlying instant.
func (u UTCTime) Time() time.Time { return u.t }

// IsZero reports whether the instant is unset.
func (u UTCTime) IsZero() bool { return u.t.IsZero() }

// String renders the instant with the fixed layout.
func (u UTCTime) String() string {
	if u.t.IsZero() {
		return ""
	}
	return u.t.UTC().Format(TimeLayout)
}

// DateKey returns the UTC calendar day of the instant.
func (u UTCTime) DateKey() string { return u.t.UTC().Format(DateLayout) }

// Before reports whether u precedes other.
func (u UTCTime) Before(other UTCTime) bool { return u.t.Before(other.t) }

// After reports whether u follows other.
func (u UTCTime) After(other UTCTime) bool { return u.t.After(other.t) }

// Equal reports instant equality.
func (u UTCTime) Equal(other UTCTime) bool { return u.t.Equal(other.t) }

// Add shifts the instant by a duration.
func (u UTCTime) Add(d time.Duration) UTCTime {
	return UTCTime{t: u.t.Add(d.Round(time.Second))}
}

// AddHours shifts the instant by a fractional number of hours, rounding to
// whole seconds so that repeated runs produce identical timestamps.
func (u UTCTime) AddHours(h float64) UTCTime {
	secs := math.Round(h * 3600)
	return UTCTime{t: u.t.Add(time.Duration(secs) * time.Second)}
}

// Sub returns the signed distance between two instants.
func (u UTCTime) Sub(other UTCTime) time.Duration { return u.t.Sub(other.t) }

// HoursSince returns the elapsed hours between other and u.
func (u UTCTime) HoursSince(other UTCTime) float64 {
	return u.t.Sub(other.t).Seconds() / 3600.0
}

// MarshalJSON emits the fixed layout, or null for the zero instant.
func (u UTCTime) MarshalJSON() ([]byte, error) {
	if u.t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(u.String())
}

// UnmarshalJSON accepts null or a string in the fixed layout.
func (u *UTCTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*u = UTCTime{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timestamp must be a JSON string: %w", err)
	}
	parsed, err := ParseUTC(s)
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// EarliestTime returns the smallest non-zero instant, or the zero instant when
// the input is empty.
func EarliestTime(times []UTCTime) UTCTime {
	var best UTCTime
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	return best
}

// LatestTime returns the largest non-zero instant.
func LatestTime(times []UTCTime) UTCTime {
	var best UTCTime
	for _, t := range times {
		if t.IsZero() {
			continue
		}
		if best.IsZero() || t.After(best) {
			best = t
		}
	}
	return best
}

// MaxTime returns the later of two instants, treating zero as unset.
func MaxTime(a, b UTCTime) UTCTime {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.After(b) {
		return a
	}
	return b
}

// Interval is a half-open time span [Start, End).
type Interval struct {
	Start UTCTime `json:"start"`
	End   UTCTime `json:"end"`
}

// Hours returns the interval length in hours.
func (iv Interval) Hours() float64 {
	if iv.Start.IsZero() || iv.End.IsZero() {
		return 0
	}
	return iv.End.HoursSince(iv.Start)
}

// Valid reports whether the interval is well formed and non-empty.
func (iv Interval) Valid() bool {
	return !iv.Start.IsZero() && !iv.End.IsZero() && iv.End.After(iv.Start)
}

// Contains reports whether the interval fully covers other.
func (iv Interval) Contains(other Interval) bool {
	if !iv.Valid() || !other.Valid() {
		return false
	}
	return !other.Start.Before(iv.Start) && !other.End.After(iv.End)
}

// Overlaps reports whether two intervals share any positive-length span.
func (iv Interval) Overlaps(other Interval) bool {
	if !iv.Valid() || !other.Valid() {
		return false
	}
	return iv.Start.Before(other.End) && other.Start.Before(iv.End)
}

// Intersect returns the shared span of two intervals; the second result is
// false when they do not overlap.
func (iv Interval) Intersect(other Interval) (Interval, bool) {
	if !iv.Overlaps(other) {
		return Interval{}, false
	}
	out := Interval{Start: iv.Start, End: iv.End}
	if other.Start.After(out.Start) {
		out.Start = other.Start
	}
	if other.End.Before(out.End) {
		out.End = other.End
	}
	return out, true
}
