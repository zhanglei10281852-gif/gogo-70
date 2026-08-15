package model

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/numeric"
)

// SystemsSchemaVersion is the accepted version of a systems document.
const SystemsSchemaVersion = 1

// End identifies which landing end a measurement was taken from. End A is the
// low-KP end of the route, end B the high-KP end.
type End string

// The two supported measurement ends.
const (
	EndA End = "A"
	EndB End = "B"
)

// Valid reports whether the end is one of the accepted values.
func (e End) Valid() bool { return e == EndA || e == EndB }

// Opposite returns the other end.
func (e End) Opposite() End {
	if e == EndA {
		return EndB
	}
	return EndA
}

// SystemsDocument is the top-level systems input file.
type SystemsDocument struct {
	Version int           `json:"version"`
	Systems []CableSystem `json:"systems"`
}

// CableSystem describes one fictional cable system end to end.
type CableSystem struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	SlackFactor     float64          `json:"slack_factor"`
	TrafficTbps     float64          `json:"traffic_tbps"`
	SingleRoute     bool             `json:"single_route"`
	LandingStations []LandingStation `json:"landing_stations"`
	Segments        []Segment        `json:"segments"`
	Repeaters       []Repeater       `json:"repeaters"`
	BranchingUnits  []BranchingUnit  `json:"branching_units"`
	ProtectionZones []ProtectionZone `json:"protection_zones"`
}

// LandingStation is a shore terminal at a fixed route position.
type LandingStation struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	KP           float64 `json:"kp"`
	Jurisdiction string  `json:"jurisdiction"`
}

// Segment is a contiguous stretch of a single cable type.
type Segment struct {
	ID             string       `json:"id"`
	StartKP        float64      `json:"start_kp"`
	EndKP          float64      `json:"end_kp"`
	CableType      string       `json:"cable_type"`
	SlackFactor    float64      `json:"slack_factor"`
	ExistingJoints int          `json:"existing_joints"`
	LossDbPerKm    float64      `json:"loss_db_per_km"`
	BurialProfile  []BurialSpan `json:"burial_profile"`
}

// RouteLengthKm is the along-route length of the segment.
func (s Segment) RouteLengthKm() float64 { return s.EndKP - s.StartKP }

// BurialSpan describes water depth and burial depth over a route interval.
type BurialSpan struct {
	StartKP      float64 `json:"start_kp"`
	EndKP        float64 `json:"end_kp"`
	DepthM       float64 `json:"depth_m"`
	BurialDepthM float64 `json:"burial_depth_m"`
	Seabed       string  `json:"seabed"`
}

// Repeater is an inline optical amplifier or equalizer housing.
type Repeater struct {
	ID               string  `json:"id"`
	KP               float64 `json:"kp"`
	Kind             string  `json:"kind"`
	GainDb           float64 `json:"gain_db"`
	CableAllowanceKm float64 `json:"cable_allowance_km"`
}

// BranchingUnit splits a trunk into a branch system.
type BranchingUnit struct {
	ID               string  `json:"id"`
	KP               float64 `json:"kp"`
	BranchSystemID   string  `json:"branch_system_id"`
	CableAllowanceKm float64 `json:"cable_allowance_km"`
}

// ProtectionZone is a jurisdictional or physical protection area along the
// route. Work inside a zone that requires a permit cannot be scheduled without
// a valid permit covering the operation.
type ProtectionZone struct {
	ID                  string  `json:"id"`
	StartKP             float64 `json:"start_kp"`
	EndKP               float64 `json:"end_kp"`
	Jurisdiction        string  `json:"jurisdiction"`
	PermitRequired      bool    `json:"permit_required"`
	AnchoringRestricted bool    `json:"anchoring_restricted"`
	RiskWeight          float64 `json:"risk_weight"`
}

// Inline is a normalized view over repeaters and branching units, used when the
// distinction does not matter (cable allowance, nearest-asset reporting).
type Inline struct {
	ID           string
	Kind         string
	KP           float64
	AllowanceKm  float64
	BranchSystem string
}

// Validate checks the document and returns every problem found.
func (d SystemsDocument) Validate() []string {
	var problems []string
	if d.Version != SystemsSchemaVersion {
		problems = append(problems, fmt.Sprintf("systems: version must be %d, got %d", SystemsSchemaVersion, d.Version))
	}
	if len(d.Systems) == 0 {
		problems = append(problems, "systems: at least one cable system is required")
	}
	seen := make(map[string]bool, len(d.Systems))
	for _, sys := range d.Systems {
		if seen[sys.ID] {
			problems = append(problems, fmt.Sprintf("systems: duplicate system id %q", sys.ID))
		}
		seen[sys.ID] = true
		problems = append(problems, sys.Validate()...)
	}
	sort.Strings(problems)
	return problems
}

// Validate checks a single system for structural consistency.
func (s CableSystem) Validate() []string {
	var problems []string
	label := func(format string, args ...any) string {
		return fmt.Sprintf("system %s: %s", s.ID, fmt.Sprintf(format, args...))
	}
	if strings.TrimSpace(s.ID) == "" {
		problems = append(problems, "system: id must not be empty")
	}
	if strings.TrimSpace(s.Name) == "" {
		problems = append(problems, label("name must not be empty"))
	}
	if s.SlackFactor != 0 && (s.SlackFactor < 1.0 || s.SlackFactor > 1.5) {
		problems = append(problems, label("slack_factor must be within [1.0, 1.5] when set, got %g", s.SlackFactor))
	}
	if !numeric.NonNegative(s.TrafficTbps) {
		problems = append(problems, label("traffic_tbps must not be negative"))
	}
	problems = append(problems, s.validateSegments(label)...)
	problems = append(problems, s.validateStations(label)...)
	problems = append(problems, s.validateInline(label)...)
	problems = append(problems, s.validateZones(label)...)
	return problems
}

func (s CableSystem) validateSegments(label func(string, ...any) string) []string {
	var problems []string
	if len(s.Segments) == 0 {
		return append(problems, label("at least one segment is required"))
	}
	segs := s.SortedSegments()
	seen := make(map[string]bool, len(segs))
	for i, seg := range segs {
		if strings.TrimSpace(seg.ID) == "" {
			problems = append(problems, label("segment %d has an empty id", i))
		}
		if seen[seg.ID] {
			problems = append(problems, label("duplicate segment id %q", seg.ID))
		}
		seen[seg.ID] = true
		if seg.EndKP <= seg.StartKP {
			problems = append(problems, label("segment %s end_kp must exceed start_kp", seg.ID))
		}
		if seg.SlackFactor != 0 && (seg.SlackFactor < 1.0 || seg.SlackFactor > 1.5) {
			problems = append(problems, label("segment %s slack_factor must be within [1.0, 1.5] when set", seg.ID))
		}
		if seg.ExistingJoints < 0 {
			problems = append(problems, label("segment %s existing_joints must not be negative", seg.ID))
		}
		if !numeric.NonNegative(seg.LossDbPerKm) {
			problems = append(problems, label("segment %s loss_db_per_km must not be negative", seg.ID))
		}
		if strings.TrimSpace(seg.CableType) == "" {
			problems = append(problems, label("segment %s cable_type must not be empty", seg.ID))
		}
		if i > 0 && !numeric.AlmostEqual(segs[i-1].EndKP, seg.StartKP, 1e-9) {
			problems = append(problems, label("segment %s starts at KP %g but the previous segment ends at KP %g",
				seg.ID, seg.StartKP, segs[i-1].EndKP))
		}
		problems = append(problems, validateBurial(seg, label)...)
	}
	if segs[0].StartKP < 0 {
		problems = append(problems, label("first segment must not start before KP 0"))
	}
	return problems
}

func validateBurial(seg Segment, label func(string, ...any) string) []string {
	var problems []string
	spans := make([]BurialSpan, len(seg.BurialProfile))
	copy(spans, seg.BurialProfile)
	sort.Slice(spans, func(i, j int) bool { return spans[i].StartKP < spans[j].StartKP })
	for i, span := range spans {
		if span.EndKP <= span.StartKP {
			problems = append(problems, label("segment %s burial span starting at KP %g must have end_kp > start_kp", seg.ID, span.StartKP))
		}
		if span.StartKP < seg.StartKP-1e-9 || span.EndKP > seg.EndKP+1e-9 {
			problems = append(problems, label("segment %s burial span [%g, %g] falls outside the segment", seg.ID, span.StartKP, span.EndKP))
		}
		if !numeric.Positive(span.DepthM) {
			problems = append(problems, label("segment %s burial span [%g, %g] depth_m must be positive", seg.ID, span.StartKP, span.EndKP))
		}
		if !numeric.NonNegative(span.BurialDepthM) {
			problems = append(problems, label("segment %s burial span [%g, %g] burial_depth_m must not be negative", seg.ID, span.StartKP, span.EndKP))
		}
		if i > 0 && spans[i-1].EndKP > span.StartKP+1e-9 {
			problems = append(problems, label("segment %s burial spans overlap at KP %g", seg.ID, span.StartKP))
		}
	}
	return problems
}

func (s CableSystem) validateStations(label func(string, ...any) string) []string {
	var problems []string
	if len(s.LandingStations) < 2 {
		problems = append(problems, label("at least two landing stations are required"))
	}
	start, end := s.RouteBounds()
	seen := make(map[string]bool, len(s.LandingStations))
	for _, st := range s.LandingStations {
		if strings.TrimSpace(st.ID) == "" {
			problems = append(problems, label("landing station id must not be empty"))
		}
		if seen[st.ID] {
			problems = append(problems, label("duplicate landing station id %q", st.ID))
		}
		seen[st.ID] = true
		if strings.TrimSpace(st.Jurisdiction) == "" {
			problems = append(problems, label("landing station %s jurisdiction must not be empty", st.ID))
		}
		if st.KP < start-1e-9 || st.KP > end+1e-9 {
			problems = append(problems, label("landing station %s KP %g is outside the route [%g, %g]", st.ID, st.KP, start, end))
		}
	}
	return problems
}

func (s CableSystem) validateInline(label func(string, ...any) string) []string {
	var problems []string
	start, end := s.RouteBounds()
	seen := make(map[string]bool)
	for _, in := range s.Inlines() {
		if strings.TrimSpace(in.ID) == "" {
			problems = append(problems, label("inline asset id must not be empty"))
		}
		if seen[in.ID] {
			problems = append(problems, label("duplicate inline asset id %q", in.ID))
		}
		seen[in.ID] = true
		if in.KP < start-1e-9 || in.KP > end+1e-9 {
			problems = append(problems, label("inline asset %s KP %g is outside the route [%g, %g]", in.ID, in.KP, start, end))
		}
		if !numeric.NonNegative(in.AllowanceKm) {
			problems = append(problems, label("inline asset %s cable_allowance_km must not be negative", in.ID))
		}
	}
	for _, rep := range s.Repeaters {
		if rep.Kind != "repeater" && rep.Kind != "equalizer" {
			problems = append(problems, label("repeater %s kind must be repeater or equalizer, got %q", rep.ID, rep.Kind))
		}
		if !numeric.NonNegative(rep.GainDb) {
			problems = append(problems, label("repeater %s gain_db must not be negative", rep.ID))
		}
	}
	for _, bu := range s.BranchingUnits {
		if strings.TrimSpace(bu.BranchSystemID) == "" {
			problems = append(problems, label("branching unit %s branch_system_id must not be empty", bu.ID))
		}
	}
	return problems
}

func (s CableSystem) validateZones(label func(string, ...any) string) []string {
	var problems []string
	start, end := s.RouteBounds()
	zones := s.SortedZones()
	seen := make(map[string]bool, len(zones))
	for i, z := range zones {
		if strings.TrimSpace(z.ID) == "" {
			problems = append(problems, label("protection zone id must not be empty"))
		}
		if seen[z.ID] {
			problems = append(problems, label("duplicate protection zone id %q", z.ID))
		}
		seen[z.ID] = true
		if z.EndKP <= z.StartKP {
			problems = append(problems, label("protection zone %s end_kp must exceed start_kp", z.ID))
		}
		if z.StartKP < start-1e-9 || z.EndKP > end+1e-9 {
			problems = append(problems, label("protection zone %s [%g, %g] falls outside the route", z.ID, z.StartKP, z.EndKP))
		}
		if strings.TrimSpace(z.Jurisdiction) == "" {
			problems = append(problems, label("protection zone %s jurisdiction must not be empty", z.ID))
		}
		if !numeric.NonNegative(z.RiskWeight) || z.RiskWeight > 10 {
			problems = append(problems, label("protection zone %s risk_weight must be within [0, 10]", z.ID))
		}
		if i > 0 && zones[i-1].EndKP > z.StartKP+1e-9 {
			problems = append(problems, label("protection zones %s and %s overlap", zones[i-1].ID, z.ID))
		}
	}
	return problems
}

// SortedSegments returns segments ordered by start KP then id.
func (s CableSystem) SortedSegments() []Segment {
	out := make([]Segment, len(s.Segments))
	copy(out, s.Segments)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartKP != out[j].StartKP {
			return out[i].StartKP < out[j].StartKP
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SortedZones returns protection zones ordered by start KP then id.
func (s CableSystem) SortedZones() []ProtectionZone {
	out := make([]ProtectionZone, len(s.ProtectionZones))
	copy(out, s.ProtectionZones)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartKP != out[j].StartKP {
			return out[i].StartKP < out[j].StartKP
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// SortedStations returns landing stations ordered by KP then id.
func (s CableSystem) SortedStations() []LandingStation {
	out := make([]LandingStation, len(s.LandingStations))
	copy(out, s.LandingStations)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].KP != out[j].KP {
			return out[i].KP < out[j].KP
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Inlines merges repeaters and branching units into one KP-ordered list.
func (s CableSystem) Inlines() []Inline {
	out := make([]Inline, 0, len(s.Repeaters)+len(s.BranchingUnits))
	for _, r := range s.Repeaters {
		kind := r.Kind
		if kind == "" {
			kind = "repeater"
		}
		out = append(out, Inline{ID: r.ID, Kind: kind, KP: r.KP, AllowanceKm: r.CableAllowanceKm})
	}
	for _, b := range s.BranchingUnits {
		out = append(out, Inline{
			ID:           b.ID,
			Kind:         "branching_unit",
			KP:           b.KP,
			AllowanceKm:  b.CableAllowanceKm,
			BranchSystem: b.BranchSystemID,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].KP != out[j].KP {
			return out[i].KP < out[j].KP
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RouteBounds returns the first and last KP covered by segments.
func (s CableSystem) RouteBounds() (float64, float64) {
	if len(s.Segments) == 0 {
		return 0, 0
	}
	segs := s.SortedSegments()
	return segs[0].StartKP, segs[len(segs)-1].EndKP
}

// RouteLengthKm is the along-route length of the whole system.
func (s CableSystem) RouteLengthKm() float64 {
	start, end := s.RouteBounds()
	return end - start
}

// SegmentSlack returns the effective slack factor for a segment, falling back
// to the system factor and then to the supplied default.
func (s CableSystem) SegmentSlack(seg Segment, fallback float64) float64 {
	if seg.SlackFactor >= 1.0 {
		return seg.SlackFactor
	}
	if s.SlackFactor >= 1.0 {
		return s.SlackFactor
	}
	return fallback
}
