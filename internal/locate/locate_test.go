package locate

import (
	"math"
	"testing"

	"CableMend/internal/config"
	"CableMend/internal/model"
)

func fixtureIndex() *model.SystemIndex {
	sys := model.CableSystem{
		ID:          "SYS-T",
		Name:        "Test Link",
		SlackFactor: 1.05,
		LandingStations: []model.LandingStation{
			{ID: "LS-A", KP: 0, Jurisdiction: "JA"},
			{ID: "LS-B", KP: 100, Jurisdiction: "JB"},
		},
		Segments: []model.Segment{{
			ID: "SEG-1", StartKP: 0, EndKP: 100, CableType: "da", SlackFactor: 1.05, LossDbPerKm: 0.2,
			BurialProfile: []model.BurialSpan{
				{StartKP: 0, EndKP: 30, DepthM: 80, BurialDepthM: 1.2, Seabed: "sand"},
				{StartKP: 30, EndKP: 100, DepthM: 2400, BurialDepthM: 0},
			},
		}},
		ProtectionZones: []model.ProtectionZone{
			{ID: "PZ-1", StartKP: 0, EndKP: 35, Jurisdiction: "JA", PermitRequired: true, AnchoringRestricted: true, RiskWeight: 2},
		},
	}
	return model.NewSystemIndex(sys, 1.03)
}

func floatPtr(v float64) *float64 { return &v }

func evidence(id string, end model.End, cableKm float64) model.Evidence {
	return model.Evidence{
		ID:              id,
		SystemID:        "SYS-T",
		FaultID:         "F-1",
		ObservedAt:      model.MustParseUTC("2026-03-10T00:00:00Z"),
		End:             end,
		Method:          model.MethodOTDR,
		CableDistanceKm: floatPtr(cableKm),
		LossStepDb:      floatPtr(3.0),
		InsulationMohm:  floatPtr(0.3),
	}
}

func TestLocalizeCombinesBothEnds(t *testing.T) {
	cfg := config.Default()
	ix := fixtureIndex()
	// A fault at KP 20 is 21 km of cable from end A and 84 km from end B.
	records := []model.Evidence{
		evidence("EV-A", model.EndA, 21.0),
		evidence("EV-B", model.EndB, 84.0),
	}
	res, err := Localize(cfg, ix, records, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.BestKP-20) > 1e-3 {
		t.Fatalf("best KP = %v, want 20", res.BestKP)
	}
	if !res.Consistent {
		t.Fatalf("expected a consistent result, flags %v", res.Flags)
	}
	if res.SegmentID != "SEG-1" {
		t.Fatalf("segment = %q", res.SegmentID)
	}
	if res.FaultClass != model.ClassShunt {
		t.Fatalf("fault class = %q, want shunt", res.FaultClass)
	}
	if res.WindowLowKP >= res.BestKP || res.WindowHighKP <= res.BestKP {
		t.Fatalf("window %v..%v does not bracket %v", res.WindowLowKP, res.WindowHighKP, res.BestKP)
	}
	if len(res.Zones) != 1 || res.Zones[0].ID != "PZ-1" {
		t.Fatalf("zones = %+v", res.Zones)
	}
	if !res.Depth.Known || res.Depth.DepthM != 80 {
		t.Fatalf("depth = %+v", res.Depth)
	}
	if res.BuriedLengthKm <= 0 {
		t.Fatal("expected the window to include buried cable")
	}
}

func TestLocalizeFlagsEndDisagreement(t *testing.T) {
	cfg := config.Default()
	ix := fixtureIndex()
	records := []model.Evidence{
		evidence("EV-A", model.EndA, 21.0),
		evidence("EV-B", model.EndB, 70.0),
	}
	res, err := Localize(cfg, ix, records, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Consistent {
		t.Fatal("expected the result to be flagged inconsistent")
	}
	if !hasFlag(res.Flags, FlagEndDisagreement) {
		t.Fatalf("flags = %v, want %s", res.Flags, FlagEndDisagreement)
	}
	if res.EndDisagreementKm <= cfg.Localization.DisagreementToleranceKm {
		t.Fatalf("disagreement = %v, expected above tolerance", res.EndDisagreementKm)
	}
	if !hasFlag(res.Flags, FlagOutlier) {
		t.Fatalf("flags = %v, want an outlier flag as well", res.Flags)
	}
}

func TestLocalizeFlagsSingleEnd(t *testing.T) {
	cfg := config.Default()
	res, err := Localize(cfg, fixtureIndex(), []model.Evidence{evidence("EV-A", model.EndA, 21.0)}, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasFlag(res.Flags, FlagSingleEnd) {
		t.Fatalf("flags = %v, want %s", res.Flags, FlagSingleEnd)
	}
	if len(res.EndsUsed) != 1 || res.EndsUsed[0] != "A" {
		t.Fatalf("ends used = %v", res.EndsUsed)
	}
}

func TestLocalizeWeightsOTDRAboveResistance(t *testing.T) {
	cfg := config.Default()
	ix := fixtureIndex()
	resistance := model.Evidence{
		ID:                "EV-R",
		SystemID:          "SYS-T",
		FaultID:           "F-1",
		ObservedAt:        model.MustParseUTC("2026-03-10T01:00:00Z"),
		End:               model.EndA,
		Method:            model.MethodResistance,
		ResistanceOhm:     floatPtr(27.0),
		ConductorOhmPerKm: floatPtr(1.0),
		InsulationMohm:    floatPtr(0.2),
	}
	res, err := Localize(cfg, ix, []model.Evidence{evidence("EV-A", model.EndA, 21.0), resistance}, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The optical estimate sits at KP 20 and the resistance estimate near KP 25.7;
	// inverse-variance weighting must stay close to the optical value.
	if res.BestKP > 21 {
		t.Fatalf("best KP = %v, expected the optical estimate to dominate", res.BestKP)
	}
	var optical, electrical Estimate
	for _, est := range res.Estimates {
		if est.EvidenceID == "EV-A" {
			optical = est
		} else {
			electrical = est
		}
	}
	if optical.Weight <= electrical.Weight {
		t.Fatalf("optical weight %v should exceed electrical weight %v", optical.Weight, electrical.Weight)
	}
}

func TestLocalizeClampsMeasurementBeyondCableLength(t *testing.T) {
	cfg := config.Default()
	ix := fixtureIndex()
	res, err := Localize(cfg, ix, []model.Evidence{evidence("EV-A", model.EndA, 500)}, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasFlag(res.Flags, FlagClampedMeasurement) {
		t.Fatalf("flags = %v, want %s", res.Flags, FlagClampedMeasurement)
	}
	if res.BestKP > ix.RouteEndKP()+1e-9 {
		t.Fatalf("best KP = %v, must stay on the route", res.BestKP)
	}
	if res.WindowHighKP > ix.RouteEndKP()+1e-9 {
		t.Fatalf("window high = %v, must stay on the route", res.WindowHighKP)
	}
}

func TestLocalizeRejectsForeignEvidence(t *testing.T) {
	cfg := config.Default()
	rec := evidence("EV-X", model.EndA, 10)
	rec.SystemID = "SYS-OTHER"
	if _, err := Localize(cfg, fixtureIndex(), []model.Evidence{rec}, "F-1"); err == nil {
		t.Fatal("expected an error for evidence from another system")
	}
}

func TestLocalizeIsDeterministicUnderInputOrder(t *testing.T) {
	cfg := config.Default()
	ix := fixtureIndex()
	forward := []model.Evidence{evidence("EV-A", model.EndA, 21.0), evidence("EV-B", model.EndB, 84.2)}
	reverse := []model.Evidence{forward[1], forward[0]}
	first, err := Localize(cfg, ix, forward, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := Localize(cfg, ix, reverse, "F-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.BestKP != second.BestKP || first.WindowLowKP != second.WindowLowKP {
		t.Fatalf("input order changed the result: %v vs %v", first.BestKP, second.BestKP)
	}
}

func TestLocalizeAllGroupsByFault(t *testing.T) {
	cfg := config.Default()
	doc := model.SystemsDocument{Version: model.SystemsSchemaVersion, Systems: []model.CableSystem{fixtureIndex().System()}}
	set := model.NewSystemSet(doc, cfg.Localization.DefaultSlackFactor)
	second := evidence("EV-C", model.EndA, 60)
	second.FaultID = "F-2"
	results, err := LocalizeAll(cfg, set, []model.Evidence{second, evidence("EV-A", model.EndA, 21)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].FaultID != "F-1" || results[1].FaultID != "F-2" {
		t.Fatalf("results are not ordered by fault: %v, %v", results[0].FaultID, results[1].FaultID)
	}
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}
