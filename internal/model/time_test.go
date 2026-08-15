package model

import (
	"encoding/json"
	"testing"
)

func TestParseUTCAcceptsOnlyTheFixedLayout(t *testing.T) {
	if _, err := ParseUTC("2026-03-10T04:05:06Z"); err != nil {
		t.Fatalf("expected the fixed layout to parse: %v", err)
	}
	for _, bad := range []string{
		"2026-03-10T04:05:06+02:00",
		"2026-03-10T04:05:06.500Z",
		"2026-03-10 04:05:06Z",
		"2026-03-10",
		"",
	} {
		if _, err := ParseUTC(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestUTCTimeJSONRoundTrip(t *testing.T) {
	var wrapper struct {
		At UTCTime `json:"at"`
	}
	if err := json.Unmarshal([]byte(`{"at":"2026-03-10T04:05:06Z"}`), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wrapper.At.String() != "2026-03-10T04:05:06Z" {
		t.Fatalf("round trip = %q", wrapper.At.String())
	}
	data, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"at":"2026-03-10T04:05:06Z"}` {
		t.Fatalf("marshal = %s", data)
	}
	if err := json.Unmarshal([]byte(`{"at":"not a time"}`), &wrapper); err == nil {
		t.Fatal("expected an invalid timestamp to fail")
	}
}

func TestAddHoursRoundsToWholeSeconds(t *testing.T) {
	base := MustParseUTC("2026-03-10T00:00:00Z")
	got := base.AddHours(1.0/3600.0 + 0.4/3600.0)
	if got.String() != "2026-03-10T00:00:01Z" {
		t.Fatalf("AddHours = %s, want a whole second", got)
	}
	if hours := got.HoursSince(base); hours <= 0 {
		t.Fatalf("HoursSince = %v, want positive", hours)
	}
}

func TestIntervalOperations(t *testing.T) {
	outer := Interval{Start: MustParseUTC("2026-03-10T00:00:00Z"), End: MustParseUTC("2026-03-12T00:00:00Z")}
	inner := Interval{Start: MustParseUTC("2026-03-10T06:00:00Z"), End: MustParseUTC("2026-03-11T06:00:00Z")}
	late := Interval{Start: MustParseUTC("2026-03-11T18:00:00Z"), End: MustParseUTC("2026-03-13T00:00:00Z")}
	if !outer.Contains(inner) {
		t.Fatal("outer should contain inner")
	}
	if outer.Contains(late) {
		t.Fatal("outer should not contain a span that ends later")
	}
	if !outer.Overlaps(late) {
		t.Fatal("outer should overlap late")
	}
	shared, ok := outer.Intersect(late)
	if !ok || shared.Hours() != 6 {
		t.Fatalf("intersection = %+v (%v h)", shared, shared.Hours())
	}
	if (Interval{}).Valid() {
		t.Fatal("the zero interval must not be valid")
	}
}

func TestEarliestAndLatestIgnoreZero(t *testing.T) {
	times := []UTCTime{{}, MustParseUTC("2026-03-11T00:00:00Z"), MustParseUTC("2026-03-10T00:00:00Z")}
	if EarliestTime(times).String() != "2026-03-10T00:00:00Z" {
		t.Fatalf("earliest = %s", EarliestTime(times))
	}
	if LatestTime(times).String() != "2026-03-11T00:00:00Z" {
		t.Fatalf("latest = %s", LatestTime(times))
	}
	if !MaxTime(UTCTime{}, times[1]).Equal(times[1]) {
		t.Fatal("MaxTime should ignore the zero instant")
	}
}
