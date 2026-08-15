package model

import (
	"fmt"
	"sort"
	"strings"
)

// PermitsSchemaVersion is the accepted version of a permits document.
const PermitsSchemaVersion = 1

// Operation is a permitted at-sea activity. Operations map one-to-one onto the
// on-site repair stages.
type Operation string

// The permitted operations.
const (
	OpSurvey     Operation = "survey"
	OpCutAndHold Operation = "cut_and_hold"
	OpSplice     Operation = "splice"
	OpTest       Operation = "test"
	OpBurial     Operation = "burial"
)

// AllOperations lists every operation in canonical order.
func AllOperations() []Operation {
	return []Operation{OpSurvey, OpCutAndHold, OpSplice, OpTest, OpBurial}
}

// Valid reports whether the operation is known.
func (o Operation) Valid() bool {
	for _, known := range AllOperations() {
		if known == o {
			return true
		}
	}
	return false
}

// PermitsDocument is the permits input file.
type PermitsDocument struct {
	Version int      `json:"version"`
	Permits []Permit `json:"permits"`
}

// Permit authorizes a set of operations inside one protection zone for a bounded
// interval.
type Permit struct {
	ID           string      `json:"id"`
	SystemID     string      `json:"system_id"`
	ZoneID       string      `json:"zone_id"`
	Jurisdiction string      `json:"jurisdiction"`
	ValidFrom    UTCTime     `json:"valid_from"`
	ValidTo      UTCTime     `json:"valid_to"`
	Operations   []Operation `json:"operations"`
	Reference    string      `json:"reference"`
}

// Window returns the permit validity interval.
func (p Permit) Window() Interval { return Interval{Start: p.ValidFrom, End: p.ValidTo} }

// Allows reports whether the permit authorizes an operation.
func (p Permit) Allows(op Operation) bool {
	for _, have := range p.Operations {
		if have == op {
			return true
		}
	}
	return false
}

// AllowsAll reports whether the permit authorizes every requested operation.
func (p Permit) AllowsAll(ops []Operation) bool {
	for _, op := range ops {
		if !p.Allows(op) {
			return false
		}
	}
	return true
}

// Validate checks the permits document.
func (d PermitsDocument) Validate() []string {
	var problems []string
	if d.Version != PermitsSchemaVersion {
		problems = append(problems, fmt.Sprintf("permits: version must be %d, got %d", PermitsSchemaVersion, d.Version))
	}
	seen := make(map[string]bool, len(d.Permits))
	for _, p := range d.Permits {
		if strings.TrimSpace(p.ID) == "" {
			problems = append(problems, "permits: permit id must not be empty")
			continue
		}
		if seen[p.ID] {
			problems = append(problems, fmt.Sprintf("permits: duplicate permit id %q", p.ID))
		}
		seen[p.ID] = true
		problems = append(problems, p.Validate()...)
	}
	sort.Strings(problems)
	return problems
}

// Validate checks one permit.
func (p Permit) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("permit %s: %s", p.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(p.SystemID) == "" {
		add("system_id must not be empty")
	}
	if strings.TrimSpace(p.ZoneID) == "" {
		add("zone_id must not be empty")
	}
	if strings.TrimSpace(p.Jurisdiction) == "" {
		add("jurisdiction must not be empty")
	}
	if p.ValidFrom.IsZero() {
		add("valid_from must be set")
	}
	if p.ValidTo.IsZero() {
		add("valid_to must be set")
	}
	if !p.ValidFrom.IsZero() && !p.ValidTo.IsZero() && !p.ValidTo.After(p.ValidFrom) {
		add("valid_to must be after valid_from")
	}
	if len(p.Operations) == 0 {
		add("at least one operation must be listed")
	}
	seen := make(map[Operation]bool, len(p.Operations))
	for _, op := range p.Operations {
		if !op.Valid() {
			add("unknown operation %q", string(op))
			continue
		}
		if seen[op] {
			add("operation %s listed twice", string(op))
		}
		seen[op] = true
	}
	return problems
}

// SortPermits orders permits by validity start, then id.
func SortPermits(permits []Permit) []Permit {
	out := make([]Permit, len(permits))
	copy(out, permits)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ValidFrom.Equal(out[j].ValidFrom) {
			return out[i].ValidFrom.Before(out[j].ValidFrom)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// PermitsForZone returns the permits applying to one system and zone.
func PermitsForZone(permits []Permit, systemID, zoneID string) []Permit {
	out := make([]Permit, 0, len(permits))
	for _, p := range permits {
		if p.SystemID == systemID && p.ZoneID == zoneID {
			out = append(out, p)
		}
	}
	return SortPermits(out)
}

// OperationNames renders operations as strings for output documents.
func OperationNames(ops []Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op))
	}
	return out
}
