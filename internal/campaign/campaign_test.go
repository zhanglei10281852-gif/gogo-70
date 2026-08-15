package campaign

import (
	"testing"

	"CableMend/internal/config"
	"CableMend/internal/locate"
	"CableMend/internal/model"
)

func systemsDoc() model.SystemsDocument {
	segment := func(id string) model.Segment {
		return model.Segment{
			ID: id, StartKP: 0, EndKP: 100, CableType: "da", SlackFactor: 1.05, LossDbPerKm: 0.2,
			BurialProfile: []model.BurialSpan{{StartKP: 0, EndKP: 100, DepthM: 1200, BurialDepthM: 0}},
		}
	}
	return model.SystemsDocument{
		Version: model.SystemsSchemaVersion,
		Systems: []model.CableSystem{
			{
				ID: "SYS-A", Name: "Alpha Link", SlackFactor: 1.05, TrafficTbps: 20,
				LandingStations: []model.LandingStation{{ID: "LS-A1", KP: 0, Jurisdiction: "JA"}, {ID: "LS-A2", KP: 100, Jurisdiction: "JA"}},
				Segments:        []model.Segment{segment("SEG-A")},
			},
			{
				ID: "SYS-B", Name: "Bravo Link", SlackFactor: 1.05, TrafficTbps: 5, SingleRoute: true,
				LandingStations: []model.LandingStation{{ID: "LS-B1", KP: 0, Jurisdiction: "JB"}, {ID: "LS-B2", KP: 100, Jurisdiction: "JB"}},
				Segments:        []model.Segment{segment("SEG-B")},
			},
		},
	}
}

func assetsDoc(spareCableKm float64) model.AssetsDocument {
	return model.AssetsDocument{
		Version: model.AssetsSchemaVersion,
		Vessels: []model.Vessel{{
			ID: "CS-ONE", Name: "One", StationDepotID: "DEP-A", TransitSpeedKn: 14, MobilizationHours: 8,
			SpareCableKm: spareCableKm, SpareJoints: 8, HasROV: true, HasAUV: true, MaxWorkingDepthM: 3000,
			MaxSeaStateM: 2.5, DayRateUnits: 100, AvailableFrom: model.MustParseUTC("2026-03-10T00:00:00Z"),
		}},
		Depots: []model.Depot{{
			ID: "DEP-A", Name: "Alpha", Jurisdiction: "JA", SpareCableKm: 500, SpareJoints: 50,
			Access: []model.DepotAccess{
				{SystemID: "SYS-A", ReferenceKP: 0, DistanceNm: 20},
				{SystemID: "SYS-B", ReferenceKP: 0, DistanceNm: 40},
			},
		}},
	}
}

func observations(systems []string, hours int) []model.Observation {
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	var out []model.Observation
	for _, sys := range systems {
		for i := 0; i < hours; i++ {
			out = append(out, model.Observation{
				SystemID: sys, HourStart: start.AddHours(float64(i)),
				WaveHeightM: 1.0, WindSpeedKn: 10, VisibilityKm: 12,
			})
		}
	}
	return out
}

func localizationFor(t *testing.T, set *model.SystemSet, systemID, faultID string, cableKm float64) locate.Result {
	t.Helper()
	ix, err := set.MustGet(systemID)
	if err != nil {
		t.Fatalf("system: %v", err)
	}
	dist := cableKm
	rec := model.Evidence{
		ID: "EV-" + faultID, SystemID: systemID, FaultID: faultID,
		ObservedAt: model.MustParseUTC("2026-03-10T00:00:00Z"), End: model.EndA,
		Method: model.MethodOTDR, CableDistanceKm: &dist,
	}
	res, err := locate.Localize(config.Default(), ix, []model.Evidence{rec}, faultID)
	if err != nil {
		t.Fatalf("localize: %v", err)
	}
	return res
}

func baseRequest(t *testing.T) Request {
	t.Helper()
	cfg := config.Default()
	set := model.NewSystemSet(systemsDoc(), cfg.Localization.DefaultSlackFactor)
	return Request{
		Faults: []model.FaultRecord{
			{ID: "F-LOW", SystemID: "SYS-B", ReportedAt: model.MustParseUTC("2026-03-10T00:00:00Z"), DeclaredPriority: 10, TrafficTbps: 5, SingleRoute: true},
			{ID: "F-HIGH", SystemID: "SYS-A", ReportedAt: model.MustParseUTC("2026-03-10T00:00:00Z"), DeclaredPriority: 90, TrafficTbps: 20},
		},
		Localizations: map[string]locate.Result{
			"F-HIGH": localizationFor(t, set, "SYS-A", "F-HIGH", 42),
			"F-LOW":  localizationFor(t, set, "SYS-B", "F-LOW", 63),
		},
		Systems:      set,
		Assets:       assetsDoc(200),
		Observations: observations([]string{"SYS-A", "SYS-B"}, 400),
		AsOf:         model.MustParseUTC("2026-03-10T00:00:00Z"),
	}
}

func TestPlanOrdersFaultsByPriority(t *testing.T) {
	result, err := Plan(config.Default(), baseRequest(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Priorities) != 2 {
		t.Fatalf("priorities = %d", len(result.Priorities))
	}
	if result.Priorities[0].FaultID != "F-HIGH" {
		t.Fatalf("highest priority = %q, want F-HIGH", result.Priorities[0].FaultID)
	}
	if result.Priorities[1].Score >= result.Priorities[0].Score {
		t.Fatalf("scores are not ordered: %v", result.Priorities)
	}
	if !result.Priorities[1].SingleRoute {
		t.Fatal("the single-route bonus input should be reported")
	}
	if len(result.Assignments) != 2 {
		t.Fatalf("assignments = %d, want 2", len(result.Assignments))
	}
	if result.Assignments[0].FaultID != "F-HIGH" {
		t.Fatalf("first assignment = %q", result.Assignments[0].FaultID)
	}
}

func TestPlanResolvesVesselContention(t *testing.T) {
	result, err := Plan(config.Default(), baseRequest(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	first, second := result.Assignments[0], result.Assignments[1]
	if first.VesselID != second.VesselID {
		t.Fatalf("expected the single vessel to take both jobs, got %q and %q", first.VesselID, second.VesselID)
	}
	if second.StartAt.Before(first.ReleaseAt) {
		t.Fatalf("second job starts %s before the vessel is released at %s", second.StartAt, first.ReleaseAt)
	}
	if len(second.Contention) != 1 || second.Contention[0].FromFaultID != first.FaultID {
		t.Fatalf("contention not recorded: %+v", second.Contention)
	}
	if len(result.Vessels) != 1 || result.Vessels[0].Assignments != 2 {
		t.Fatalf("vessel utilization = %+v", result.Vessels)
	}
	if result.MakespanHours <= 0 {
		t.Fatalf("makespan = %v", result.MakespanHours)
	}
}

func TestPlanConsumesVesselAndDepotStock(t *testing.T) {
	req := baseRequest(t)
	req.Assets = assetsDoc(6)
	result, err := Plan(config.Default(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Assignments) != 2 {
		t.Fatalf("assignments = %d", len(result.Assignments))
	}
	if len(result.DepotDraws) == 0 {
		t.Fatal("expected the depot to supply cable once the vessel stock runs down")
	}
	if result.DepotDraws[0].CableKm <= 0 {
		t.Fatalf("depot draw = %+v", result.DepotDraws[0])
	}
	if result.TotalCableKm <= 0 || result.TotalJoints != 4 {
		t.Fatalf("totals = %v km, %d joints", result.TotalCableKm, result.TotalJoints)
	}
}

func TestPlanReportsFaultsWithoutLocalization(t *testing.T) {
	req := baseRequest(t)
	delete(req.Localizations, "F-LOW")
	result, err := Plan(config.Default(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(result.Unassigned) != 1 || result.Unassigned[0].FaultID != "F-LOW" {
		t.Fatalf("unassigned = %+v", result.Unassigned)
	}
	if len(result.Unassigned[0].Reasons) == 0 {
		t.Fatal("expected a reason")
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	cfg := config.Default()
	first, err := Plan(cfg, baseRequest(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	second, err := Plan(cfg, baseRequest(t))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if first.TotalCost != second.TotalCost || first.MakespanHours != second.MakespanHours {
		t.Fatal("campaign planning must be deterministic")
	}
	for i := range first.Assignments {
		if first.Assignments[i].FaultID != second.Assignments[i].FaultID {
			t.Fatal("assignment order must be deterministic")
		}
		if !first.Assignments[i].StartAt.Equal(second.Assignments[i].StartAt) {
			t.Fatal("assignment timing must be deterministic")
		}
	}
}

func TestPlanRequiresFaultsAndAsOf(t *testing.T) {
	cfg := config.Default()
	req := baseRequest(t)
	req.Faults = nil
	if _, err := Plan(cfg, req); err == nil {
		t.Fatal("expected an error without faults")
	}
	req = baseRequest(t)
	req.AsOf = model.UTCTime{}
	if _, err := Plan(cfg, req); err == nil {
		t.Fatal("expected an error without an as-of instant")
	}
}
