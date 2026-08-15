package model

import (
	"math"
	"testing"
)

// testSystem builds a two-segment system with one repeater that carries a cable
// allowance, which is the interesting case for distance conversion.
func testSystem() CableSystem {
	return CableSystem{
		ID:          "SYS-T",
		Name:        "Test Link",
		SlackFactor: 1.02,
		LandingStations: []LandingStation{
			{ID: "LS-A", Name: "A", KP: 0, Jurisdiction: "JA"},
			{ID: "LS-B", Name: "B", KP: 200, Jurisdiction: "JB"},
		},
		Segments: []Segment{
			{
				ID: "SEG-1", StartKP: 0, EndKP: 100, CableType: "da", SlackFactor: 1.05,
				LossDbPerKm: 0.2,
				BurialProfile: []BurialSpan{
					{StartKP: 0, EndKP: 20, DepthM: 50, BurialDepthM: 1.5, Seabed: "sand"},
					{StartKP: 20, EndKP: 100, DepthM: 900, BurialDepthM: 0},
				},
			},
			{
				ID: "SEG-2", StartKP: 100, EndKP: 200, CableType: "lw", SlackFactor: 1.03,
				ExistingJoints: 2, LossDbPerKm: 0.18,
				BurialProfile: []BurialSpan{
					{StartKP: 100, EndKP: 200, DepthM: 2600, BurialDepthM: 0},
				},
			},
		},
		Repeaters: []Repeater{
			{ID: "REP-1", KP: 60, Kind: "repeater", GainDb: 10, CableAllowanceKm: 0.4},
		},
		ProtectionZones: []ProtectionZone{
			{ID: "PZ-1", StartKP: 0, EndKP: 25, Jurisdiction: "JA", PermitRequired: true, AnchoringRestricted: true, RiskWeight: 2},
			{ID: "PZ-2", StartKP: 150, EndKP: 200, Jurisdiction: "JB", RiskWeight: 1},
		},
	}
}

func TestSystemDocumentValidates(t *testing.T) {
	doc := SystemsDocument{Version: SystemsSchemaVersion, Systems: []CableSystem{testSystem()}}
	if problems := doc.Validate(); len(problems) != 0 {
		t.Fatalf("expected a valid document, got %v", problems)
	}
}

func TestSystemDocumentRejectsGaps(t *testing.T) {
	sys := testSystem()
	sys.Segments[1].StartKP = 105
	doc := SystemsDocument{Version: SystemsSchemaVersion, Systems: []CableSystem{sys}}
	problems := doc.Validate()
	if len(problems) == 0 {
		t.Fatal("expected a contiguity problem")
	}
}

func TestCableLengthIncludesSlackAndAllowance(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	want := 100*1.05 + 100*1.03 + 0.4
	if math.Abs(ix.CableLengthKm()-want) > 1e-9 {
		t.Fatalf("cable length = %v, want %v", ix.CableLengthKm(), want)
	}
	if ix.RouteLengthKm() != 200 {
		t.Fatalf("route length = %v, want 200", ix.RouteLengthKm())
	}
}

func TestKPFromCableDistanceBothEnds(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	// KP 80 is 60 km at 1.05 slack, plus the repeater allowance, plus 20 km at 1.05.
	wantCableFromA := 60*1.05 + 0.4 + 20*1.05
	convA, err := ix.KPFromCableDistance(EndA, wantCableFromA)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(convA.KP-80) > 1e-6 {
		t.Fatalf("end A conversion = %v, want 80", convA.KP)
	}
	cableFromB, err := ix.CableDistanceFromKP(EndB, 80)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantFromB := 100*1.03 + 20*1.05
	if math.Abs(cableFromB-wantFromB) > 1e-9 {
		t.Fatalf("cable from B = %v, want %v", cableFromB, wantFromB)
	}
	convB, err := ix.KPFromCableDistance(EndB, cableFromB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(convB.KP-80) > 1e-6 {
		t.Fatalf("end B conversion = %v, want 80", convB.KP)
	}
}

func TestCableDistancesSumToCableLength(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	for _, kp := range []float64{0, 15, 59.9, 60, 60.1, 120, 199.5, 200} {
		fromA, err := ix.CableDistanceFromKP(EndA, kp)
		if err != nil {
			t.Fatalf("end A at KP %v: %v", kp, err)
		}
		fromB, err := ix.CableDistanceFromKP(EndB, kp)
		if err != nil {
			t.Fatalf("end B at KP %v: %v", kp, err)
		}
		if math.Abs(fromA+fromB-ix.CableLengthKm()) > 1e-6 {
			t.Fatalf("KP %v: %v + %v != %v", kp, fromA, fromB, ix.CableLengthKm())
		}
	}
}

func TestKPFromCableDistanceInsideRepeaterAllowance(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	conv, err := ix.KPFromCableDistance(EndA, 60*1.05+0.2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.InlineID != "REP-1" {
		t.Fatalf("inline id = %q, want REP-1", conv.InlineID)
	}
	if math.Abs(conv.KP-60) > 1e-9 {
		t.Fatalf("KP = %v, want 60", conv.KP)
	}
}

func TestKPFromCableDistanceClampsBeyondEnd(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	conv, err := ix.KPFromCableDistance(EndA, ix.CableLengthKm()+50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !conv.Clamped {
		t.Fatal("expected the conversion to be clamped")
	}
	if math.Abs(conv.KP-200) > 1e-9 {
		t.Fatalf("KP = %v, want 200", conv.KP)
	}
}

func TestSegmentZoneAndDepthLookups(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	seg, ok := ix.SegmentAt(150)
	if !ok || seg.ID != "SEG-2" {
		t.Fatalf("segment at 150 = %v (%v), want SEG-2", seg.ID, ok)
	}
	if _, ok := ix.SegmentAt(200); !ok {
		t.Fatal("the far landing should resolve to the last segment")
	}
	zones := ix.ZonesIn(10, 30)
	if len(zones) != 1 || zones[0].ID != "PZ-1" {
		t.Fatalf("zones = %v, want PZ-1 only", ZoneIDs(zones))
	}
	depth := ix.MaxDepthIn(10, 30)
	if !depth.Known || depth.DepthM != 900 {
		t.Fatalf("max depth = %+v, want the deeper span", depth)
	}
	if buried := ix.BuriedLengthKm(10, 30); math.Abs(buried-10) > 1e-9 {
		t.Fatalf("buried length = %v, want 10", buried)
	}
	if buried := ix.BuriedLengthKm(120, 130); buried != 0 {
		t.Fatalf("buried length in deep water = %v, want 0", buried)
	}
}

func TestNearestInlineIsDeterministic(t *testing.T) {
	sys := testSystem()
	sys.Repeaters = append(sys.Repeaters, Repeater{ID: "REP-0", KP: 40, Kind: "repeater", GainDb: 10})
	ix := NewSystemIndex(sys, 1.03)
	inline, dist, ok := ix.NearestInline(50)
	if !ok {
		t.Fatal("expected an inline asset")
	}
	if inline.ID != "REP-0" {
		t.Fatalf("tie broken to %q, want REP-0 by identifier order", inline.ID)
	}
	if math.Abs(dist-10) > 1e-9 {
		t.Fatalf("distance = %v, want 10", dist)
	}
}

func TestExpectedSegmentLoss(t *testing.T) {
	ix := NewSystemIndex(testSystem(), 1.03)
	seg, _ := ix.SegmentByID("SEG-1")
	got := ix.ExpectedSegmentLossDb(seg, 3, 0.15)
	want := (100*1.05+0.4)*0.2 + 3*0.15
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("expected loss = %v, want %v", got, want)
	}
}

func TestSystemSetOrdering(t *testing.T) {
	doc := SystemsDocument{Version: SystemsSchemaVersion, Systems: []CableSystem{
		{ID: "SYS-Z"}, {ID: "SYS-A"},
	}}
	set := NewSystemSet(doc, 1.03)
	ids := set.IDs()
	if len(ids) != 2 || ids[0] != "SYS-A" || ids[1] != "SYS-Z" {
		t.Fatalf("ids = %v, want sorted", ids)
	}
	if _, err := set.MustGet("SYS-MISSING"); err == nil {
		t.Fatal("expected an error for an unknown system")
	}
}
