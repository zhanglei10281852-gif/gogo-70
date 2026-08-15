package model

import (
	"fmt"
	"sort"

	"CableMend/internal/numeric"
)

// Epsilon is the KP comparison tolerance in kilometres (1 mm).
const Epsilon = 1e-6

// SystemIndex is a precomputed, immutable view over a cable system that answers
// the geometric questions the rest of the tool needs.
type SystemIndex struct {
	system   CableSystem
	segments []Segment
	zones    []ProtectionZone
	inlines  []Inline
	stations []LandingStation
	fallback float64
	startKP  float64
	endKP    float64
	cableKm  float64
}

// NewSystemIndex builds the index. fallback is the configured default slack
// factor, used when neither segment nor system declares one.
func NewSystemIndex(sys CableSystem, fallback float64) *SystemIndex {
	if fallback < 1.0 {
		fallback = 1.0
	}
	ix := &SystemIndex{
		system:   sys,
		segments: sys.SortedSegments(),
		zones:    sys.SortedZones(),
		inlines:  sys.Inlines(),
		stations: sys.SortedStations(),
		fallback: fallback,
	}
	ix.startKP, ix.endKP = sys.RouteBounds()
	for _, seg := range ix.segments {
		ix.cableKm += seg.RouteLengthKm() * sys.SegmentSlack(seg, fallback)
	}
	for _, in := range ix.inlines {
		ix.cableKm += in.AllowanceKm
	}
	return ix
}

// System returns the underlying system definition.
func (ix *SystemIndex) System() CableSystem { return ix.system }

// ID returns the system identifier.
func (ix *SystemIndex) ID() string { return ix.system.ID }

// Segments returns the KP-ordered segments.
func (ix *SystemIndex) Segments() []Segment { return ix.segments }

// Zones returns the KP-ordered protection zones.
func (ix *SystemIndex) Zones() []ProtectionZone { return ix.zones }

// Inlines returns the KP-ordered inline assets.
func (ix *SystemIndex) Inlines() []Inline { return ix.inlines }

// RouteStartKP is the low-KP route boundary.
func (ix *SystemIndex) RouteStartKP() float64 { return ix.startKP }

// RouteEndKP is the high-KP route boundary.
func (ix *SystemIndex) RouteEndKP() float64 { return ix.endKP }

// RouteLengthKm is the along-route length.
func (ix *SystemIndex) RouteLengthKm() float64 { return ix.endKP - ix.startKP }

// CableLengthKm is the total installed cable length including slack and inline
// housing allowances.
func (ix *SystemIndex) CableLengthKm() float64 { return ix.cableKm }

// SlackFallback returns the configured fallback slack factor.
func (ix *SystemIndex) SlackFallback() float64 { return ix.fallback }

// SegmentAt returns the segment containing the position. The high boundary is
// inclusive for the final segment so that the far landing resolves.
func (ix *SystemIndex) SegmentAt(kp float64) (Segment, bool) {
	for i, seg := range ix.segments {
		last := i == len(ix.segments)-1
		if kp >= seg.StartKP-Epsilon && (kp < seg.EndKP-Epsilon || (last && kp <= seg.EndKP+Epsilon)) {
			return seg, true
		}
	}
	return Segment{}, false
}

// SegmentByID looks a segment up by identifier.
func (ix *SystemIndex) SegmentByID(id string) (Segment, bool) {
	for _, seg := range ix.segments {
		if seg.ID == id {
			return seg, true
		}
	}
	return Segment{}, false
}

// SlackAt returns the effective slack factor at a route position.
func (ix *SystemIndex) SlackAt(kp float64) float64 {
	if seg, ok := ix.SegmentAt(kp); ok {
		return ix.system.SegmentSlack(seg, ix.fallback)
	}
	if ix.system.SlackFactor >= 1.0 {
		return ix.system.SlackFactor
	}
	return ix.fallback
}

// ZonesAt returns every protection zone containing the position.
func (ix *SystemIndex) ZonesAt(kp float64) []ProtectionZone {
	var out []ProtectionZone
	for _, z := range ix.zones {
		if kp >= z.StartKP-Epsilon && kp <= z.EndKP+Epsilon {
			out = append(out, z)
		}
	}
	return out
}

// ZonesIn returns every protection zone overlapping the interval.
func (ix *SystemIndex) ZonesIn(lo, hi float64) []ProtectionZone {
	if hi < lo {
		lo, hi = hi, lo
	}
	var out []ProtectionZone
	for _, z := range ix.zones {
		if z.EndKP >= lo-Epsilon && z.StartKP <= hi+Epsilon {
			out = append(out, z)
		}
	}
	return out
}

// ZoneIDs returns the identifiers of the given zones in order.
func ZoneIDs(zones []ProtectionZone) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		out = append(out, z.ID)
	}
	return out
}

// Jurisdictions returns the sorted, de-duplicated jurisdictions of the zones.
func Jurisdictions(zones []ProtectionZone) []string {
	seen := make(map[string]bool, len(zones))
	var out []string
	for _, z := range zones {
		if z.Jurisdiction == "" || seen[z.Jurisdiction] {
			continue
		}
		seen[z.Jurisdiction] = true
		out = append(out, z.Jurisdiction)
	}
	sort.Strings(out)
	return out
}

// DepthSample is the seabed condition at or across a route position.
type DepthSample struct {
	DepthM       float64 `json:"depth_m"`
	BurialDepthM float64 `json:"burial_depth_m"`
	Seabed       string  `json:"seabed"`
	Known        bool    `json:"known"`
}

// DepthAt returns the seabed sample at a position.
func (ix *SystemIndex) DepthAt(kp float64) DepthSample {
	for _, seg := range ix.segments {
		for _, span := range seg.BurialProfile {
			if kp >= span.StartKP-Epsilon && kp <= span.EndKP+Epsilon {
				return DepthSample{
					DepthM:       span.DepthM,
					BurialDepthM: span.BurialDepthM,
					Seabed:       span.Seabed,
					Known:        true,
				}
			}
		}
	}
	return DepthSample{}
}

// MaxDepthIn returns the deepest sample overlapping the interval, preferring
// the deepest water because that is what constrains vessel capability.
func (ix *SystemIndex) MaxDepthIn(lo, hi float64) DepthSample {
	if hi < lo {
		lo, hi = hi, lo
	}
	var best DepthSample
	for _, seg := range ix.segments {
		for _, span := range seg.BurialProfile {
			if span.EndKP < lo-Epsilon || span.StartKP > hi+Epsilon {
				continue
			}
			if !best.Known || span.DepthM > best.DepthM {
				best = DepthSample{
					DepthM:       span.DepthM,
					BurialDepthM: span.BurialDepthM,
					Seabed:       span.Seabed,
					Known:        true,
				}
			}
		}
	}
	return best
}

// BuriedLengthKm returns the length of the interval that carries a positive
// burial requirement, which drives the re-burial stage duration.
func (ix *SystemIndex) BuriedLengthKm(lo, hi float64) float64 {
	if hi < lo {
		lo, hi = hi, lo
	}
	total := 0.0
	for _, seg := range ix.segments {
		for _, span := range seg.BurialProfile {
			if span.BurialDepthM <= 0 {
				continue
			}
			start := span.StartKP
			if lo > start {
				start = lo
			}
			end := span.EndKP
			if hi < end {
				end = hi
			}
			if end > start {
				total += end - start
			}
		}
	}
	return total
}

// NearestInline returns the closest inline asset to the position.
func (ix *SystemIndex) NearestInline(kp float64) (Inline, float64, bool) {
	if len(ix.inlines) == 0 {
		return Inline{}, 0, false
	}
	best := ix.inlines[0]
	bestDist := abs(kp - best.KP)
	for _, in := range ix.inlines[1:] {
		d := abs(kp - in.KP)
		if d < bestDist-Epsilon || (numeric.AlmostEqual(d, bestDist, Epsilon) && in.ID < best.ID) {
			best, bestDist = in, d
		}
	}
	return best, bestDist, true
}

// StationForEnd returns the landing station at the requested route end.
func (ix *SystemIndex) StationForEnd(end End) (LandingStation, bool) {
	if len(ix.stations) == 0 {
		return LandingStation{}, false
	}
	if end == EndA {
		return ix.stations[0], true
	}
	return ix.stations[len(ix.stations)-1], true
}

// Conversion is the result of translating a measured cable distance into a
// route position.
type Conversion struct {
	KP             float64 `json:"kp"`
	CableKm        float64 `json:"cable_km"`
	RouteKm        float64 `json:"route_km"`
	EffectiveSlack float64 `json:"effective_slack"`
	Clamped        bool    `json:"clamped"`
	InlineID       string  `json:"inline_id,omitempty"`
}

// leg is one uniform-slack stretch used while walking the route.
type leg struct {
	fromKP  float64
	toKP    float64
	slack   float64
	inlineA string  // inline asset located at fromKP
	allowKm float64 // cable allowance consumed at fromKP
}

// walkLegs builds the ordered list of legs from the requested end, splitting
// segments at inline assets so that housing cable allowances are accounted for
// at the correct route position.
func (ix *SystemIndex) walkLegs(end End) []leg {
	legs := make([]leg, 0, len(ix.segments)+len(ix.inlines))
	for _, seg := range ix.segments {
		slack := ix.system.SegmentSlack(seg, ix.fallback)
		cuts := make([]Inline, 0, len(ix.inlines))
		for _, in := range ix.inlines {
			if in.KP > seg.StartKP+Epsilon && in.KP < seg.EndKP-Epsilon {
				cuts = append(cuts, in)
			}
		}
		cursor := seg.StartKP
		for _, cut := range cuts {
			legs = append(legs, leg{fromKP: cursor, toKP: cut.KP, slack: slack})
			legs = append(legs, leg{fromKP: cut.KP, toKP: cut.KP, slack: slack, inlineA: cut.ID, allowKm: cut.AllowanceKm})
			cursor = cut.KP
		}
		legs = append(legs, leg{fromKP: cursor, toKP: seg.EndKP, slack: slack})
	}
	// Inline assets exactly on a segment boundary are attributed once, at the
	// boundary, so their allowance is never double counted.
	for _, in := range ix.inlines {
		onBoundary := false
		for _, seg := range ix.segments {
			if numeric.AlmostEqual(in.KP, seg.StartKP, Epsilon) || numeric.AlmostEqual(in.KP, seg.EndKP, Epsilon) {
				onBoundary = true
				break
			}
		}
		if !onBoundary || in.AllowanceKm <= 0 {
			continue
		}
		slack := ix.SlackAt(in.KP)
		legs = append(legs, leg{fromKP: in.KP, toKP: in.KP, slack: slack, inlineA: in.ID, allowKm: in.AllowanceKm})
	}
	sort.SliceStable(legs, func(i, j int) bool {
		if legs[i].fromKP != legs[j].fromKP {
			return legs[i].fromKP < legs[j].fromKP
		}
		if legs[i].toKP != legs[j].toKP {
			return legs[i].toKP < legs[j].toKP
		}
		return legs[i].inlineA < legs[j].inlineA
	})
	if end == EndB {
		reversed := make([]leg, 0, len(legs))
		for i := len(legs) - 1; i >= 0; i-- {
			l := legs[i]
			l.fromKP, l.toKP = l.toKP, l.fromKP
			reversed = append(reversed, l)
		}
		return reversed
	}
	return legs
}

// KPFromCableDistance converts a cable distance measured from one end into a
// route position, honouring per-segment slack and inline cable allowances.
func (ix *SystemIndex) KPFromCableDistance(end End, cableKm float64) (Conversion, error) {
	if !end.Valid() {
		return Conversion{}, fmt.Errorf("unknown measurement end %q", string(end))
	}
	if cableKm < 0 {
		return Conversion{}, fmt.Errorf("cable distance must not be negative, got %g", cableKm)
	}
	legs := ix.walkLegs(end)
	if len(legs) == 0 {
		return Conversion{}, fmt.Errorf("system %s has no route geometry", ix.ID())
	}
	origin := legs[0].fromKP
	cum := 0.0
	for _, l := range legs {
		if l.allowKm > 0 {
			if cum+l.allowKm >= cableKm-Epsilon {
				return Conversion{
					KP:             l.fromKP,
					CableKm:        cableKm,
					RouteKm:        abs(l.fromKP - origin),
					EffectiveSlack: effectiveSlack(cableKm, abs(l.fromKP-origin)),
					InlineID:       l.inlineA,
				}, nil
			}
			cum += l.allowKm
			continue
		}
		routeLen := abs(l.toKP - l.fromKP)
		if routeLen <= 0 {
			continue
		}
		cableLen := routeLen * l.slack
		if cum+cableLen >= cableKm-Epsilon {
			remaining := cableKm - cum
			if remaining < 0 {
				remaining = 0
			}
			advance := remaining / l.slack
			kp := l.fromKP + advance
			if end == EndB {
				kp = l.fromKP - advance
			}
			return Conversion{
				KP:             kp,
				CableKm:        cableKm,
				RouteKm:        abs(kp - origin),
				EffectiveSlack: effectiveSlack(cableKm, abs(kp-origin)),
				Clamped:        false,
			}, nil
		}
		cum += cableLen
	}
	far := legs[len(legs)-1].toKP
	return Conversion{
		KP:             far,
		CableKm:        cum,
		RouteKm:        abs(far - origin),
		EffectiveSlack: effectiveSlack(cum, abs(far-origin)),
		Clamped:        true,
	}, nil
}

// CableDistanceFromKP converts a route position into the cable distance that a
// test set at the given end would measure.
func (ix *SystemIndex) CableDistanceFromKP(end End, kp float64) (float64, error) {
	if !end.Valid() {
		return 0, fmt.Errorf("unknown measurement end %q", string(end))
	}
	if kp < ix.startKP-Epsilon || kp > ix.endKP+Epsilon {
		return 0, fmt.Errorf("KP %g is outside the route [%g, %g]", kp, ix.startKP, ix.endKP)
	}
	legs := ix.walkLegs(end)
	cum := 0.0
	for _, l := range legs {
		if l.allowKm > 0 {
			if between(l.fromKP, kp, end) {
				cum += l.allowKm
			}
			continue
		}
		lo, hi := l.fromKP, l.toKP
		if end == EndB {
			lo, hi = hi, lo
		}
		if end == EndA {
			if kp <= lo+Epsilon {
				break
			}
			upper := hi
			if kp < hi {
				upper = kp
			}
			cum += (upper - lo) * l.slack
			continue
		}
		if kp >= hi-Epsilon {
			break
		}
		lower := lo
		if kp > lo {
			lower = kp
		}
		cum += (hi - lower) * l.slack
	}
	return cum, nil
}

// between reports whether the cable allowance of an inline asset at inlineKP is
// consumed on the way from the given end to the target position. An asset lying
// exactly on the target position is attributed to the high-KP side, so the two
// end distances always add up to the installed cable length.
func between(inlineKP, kp float64, end End) bool {
	if end == EndA {
		return inlineKP < kp-Epsilon
	}
	return inlineKP >= kp-Epsilon
}

// effectiveSlack is cable length divided by route length, guarding the zero
// route case at the origin.
func effectiveSlack(cableKm, routeKm float64) float64 {
	if routeKm <= Epsilon {
		return 0
	}
	return cableKm / routeKm
}

// ClampKP constrains a position to the route.
func (ix *SystemIndex) ClampKP(kp float64) float64 {
	return numeric.Clamp(kp, ix.startKP, ix.endKP)
}

// JointsInSegment returns the number of joints already present in a segment.
func (ix *SystemIndex) JointsInSegment(segmentID string) int {
	if seg, ok := ix.SegmentByID(segmentID); ok {
		return seg.ExistingJoints
	}
	return 0
}

// ExpectedSegmentLossDb returns the fibre loss expected for a segment given a
// joint count and per-joint loss allowance.
func (ix *SystemIndex) ExpectedSegmentLossDb(seg Segment, joints int, jointLossDb float64) float64 {
	slack := ix.system.SegmentSlack(seg, ix.fallback)
	cableKm := seg.RouteLengthKm() * slack
	for _, in := range ix.inlines {
		if in.KP > seg.StartKP+Epsilon && in.KP < seg.EndKP-Epsilon {
			cableKm += in.AllowanceKm
		}
	}
	return cableKm*seg.LossDbPerKm + float64(joints)*jointLossDb
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// SystemSet is a deterministic collection of indexed systems.
type SystemSet struct {
	order   []string
	byID    map[string]*SystemIndex
	fallbck float64
}

// NewSystemSet indexes every system in a document.
func NewSystemSet(doc SystemsDocument, fallbackSlack float64) *SystemSet {
	set := &SystemSet{byID: make(map[string]*SystemIndex, len(doc.Systems)), fallbck: fallbackSlack}
	for _, sys := range doc.Systems {
		set.byID[sys.ID] = NewSystemIndex(sys, fallbackSlack)
		set.order = append(set.order, sys.ID)
	}
	sort.Strings(set.order)
	return set
}

// IDs returns the sorted system identifiers.
func (s *SystemSet) IDs() []string {
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

// Get returns the index for a system id.
func (s *SystemSet) Get(id string) (*SystemIndex, bool) {
	ix, ok := s.byID[id]
	return ix, ok
}

// MustGet returns the index or an error mentioning the known identifiers.
func (s *SystemSet) MustGet(id string) (*SystemIndex, error) {
	if ix, ok := s.byID[id]; ok {
		return ix, nil
	}
	return nil, fmt.Errorf("unknown system %q (known: %v)", id, s.IDs())
}

// Len returns the number of indexed systems.
func (s *SystemSet) Len() int { return len(s.order) }
