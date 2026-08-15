package planner

import (
	"testing"

	"CableMend/internal/config"
	"CableMend/internal/locate"
	"CableMend/internal/model"
)

func testIndex() *model.SystemIndex {
	sys := model.CableSystem{
		ID:          "SYS-T",
		Name:        "Test Link",
		SlackFactor: 1.05,
		TrafficTbps: 10,
		LandingStations: []model.LandingStation{
			{ID: "LS-A", KP: 0, Jurisdiction: "JA"},
			{ID: "LS-B", KP: 100, Jurisdiction: "JB"},
		},
		Segments: []model.Segment{{
			ID: "SEG-1", StartKP: 0, EndKP: 100, CableType: "da", SlackFactor: 1.05,
			ExistingJoints: 1, LossDbPerKm: 0.2,
			BurialProfile: []model.BurialSpan{
				{StartKP: 0, EndKP: 30, DepthM: 80, BurialDepthM: 1.2, Seabed: "sand"},
				{StartKP: 30, EndKP: 100, DepthM: 2400, BurialDepthM: 0, Seabed: "clay"},
			},
		}},
		ProtectionZones: []model.ProtectionZone{
			{ID: "PZ-1", StartKP: 0, EndKP: 35, Jurisdiction: "JA", PermitRequired: true, AnchoringRestricted: true, RiskWeight: 2},
		},
	}
	return model.NewSystemIndex(sys, 1.03)
}

func testAssets(maxDepth, spareCable float64, dayRate float64) *model.AssetSet {
	return model.NewAssetSet(model.AssetsDocument{
		Version: model.AssetsSchemaVersion,
		Vessels: []model.Vessel{{
			ID: "CS-ONE", Name: "One", HomeDepotID: "DEP-A", StationDepotID: "DEP-A",
			TransitSpeedKn: 12, MobilizationHours: 12, SpareCableKm: spareCable, SpareJoints: 6,
			HasROV: true, HasAUV: true, MaxWorkingDepthM: maxDepth, MaxSeaStateM: 2.5,
			DayRateUnits: dayRate, AvailableFrom: model.MustParseUTC("2026-03-10T00:00:00Z"),
		}},
		Depots: []model.Depot{{
			ID: "DEP-A", Name: "Alpha", Jurisdiction: "JA", SpareCableKm: 200, SpareJoints: 20,
			Access: []model.DepotAccess{{SystemID: "SYS-T", ReferenceKP: 0, DistanceNm: 30}},
		}},
	})
}

func calmObservations(hours int) []model.Observation {
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	out := make([]model.Observation, 0, hours)
	for i := 0; i < hours; i++ {
		out = append(out, model.Observation{
			SystemID: "SYS-T", HourStart: start.AddHours(float64(i)),
			WaveHeightM: 1.1, WindSpeedKn: 14, VisibilityKm: 12,
		})
	}
	return out
}

func testPermits(from, to string) []model.Permit {
	return []model.Permit{{
		ID: "PMT-1", SystemID: "SYS-T", ZoneID: "PZ-1", Jurisdiction: "JA",
		ValidFrom: model.MustParseUTC(from), ValidTo: model.MustParseUTC(to),
		Operations: model.AllOperations(),
	}}
}

func localization(t *testing.T, ix *model.SystemIndex, cableKmFromA float64) locate.Result {
	t.Helper()
	cfg := config.Default()
	dist := cableKmFromA
	rec := model.Evidence{
		ID: "EV-A", SystemID: "SYS-T", FaultID: "F-1",
		ObservedAt: model.MustParseUTC("2026-03-10T00:00:00Z"),
		End:        model.EndA, Method: model.MethodOTDR,
		CableDistanceKm: &dist,
	}
	res, err := locate.Localize(cfg, ix, []model.Evidence{rec}, "F-1")
	if err != nil {
		t.Fatalf("localize: %v", err)
	}
	return res
}

func baseRequest(t *testing.T, ix *model.SystemIndex, res locate.Result, assets *model.AssetSet) Request {
	t.Helper()
	return Request{
		Fault:        model.FaultRecord{ID: "F-1", SystemID: "SYS-T", ReportedAt: model.MustParseUTC("2026-03-10T00:00:00Z")},
		Localization: res,
		Index:        ix,
		Assets:       assets,
		Observations: calmObservations(240),
		Permits:      testPermits("2026-03-01T00:00:00Z", "2026-04-01T00:00:00Z"),
		AsOf:         model.MustParseUTC("2026-03-10T00:00:00Z"),
	}
}

func TestBuildProducesOrderedStagePlan(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21) // KP 20, inside the buried nearshore zone
	plan, err := Build(cfg, baseRequest(t, ix, res, testAssets(3000, 50, 100)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected a feasible plan, reasons %v", plan.Reasons)
	}
	if plan.Stages[0].Name != StageMobilize {
		t.Fatalf("first stage = %q", plan.Stages[0].Name)
	}
	if plan.Stages[len(plan.Stages)-1].Name != StageDemobilize {
		t.Fatalf("last stage = %q", plan.Stages[len(plan.Stages)-1].Name)
	}
	var cumulative float64
	for i, stage := range plan.Stages {
		if stage.Order != i+1 {
			t.Fatalf("stage %d has order %d", i, stage.Order)
		}
		if stage.Hours <= 0 {
			t.Fatalf("stage %s has no duration", stage.Name)
		}
		if i > 0 && !stage.StartAt.Equal(plan.Stages[i-1].EndAt) {
			t.Fatalf("stage %s does not start when the previous stage ends", stage.Name)
		}
		cumulative += stage.Hours
		if stage.CumulativeHours < cumulative-1e-6 || stage.CumulativeHours > cumulative+1e-6 {
			t.Fatalf("stage %s cumulative = %v, want %v", stage.Name, stage.CumulativeHours, cumulative)
		}
	}
	if !hasStage(plan.Stages, StageFinalBurial) {
		t.Fatal("a buried fault position must schedule final burial")
	}
	onSite := 0.0
	for _, stage := range plan.Stages {
		if stage.OnSite {
			onSite += stage.Hours
		}
	}
	if onSite < plan.Window.Hours-1e-6 || onSite > plan.Window.Hours+1e-6 {
		t.Fatalf("on-site hours %v do not match the selected window %v", onSite, plan.Window.Hours)
	}
	if !plan.Permits.Satisfied {
		t.Fatal("expected the permit assessment to be satisfied")
	}
	if plan.Window.Start.Before(plan.Stages[0].StartAt) {
		t.Fatal("the work window must not start before mobilization")
	}
}

func TestBuildOmitsBurialInDeepWater(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 63) // KP 60, deep water, no burial
	plan, err := Build(cfg, baseRequest(t, ix, res, testAssets(3000, 50, 100)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected a feasible plan, reasons %v", plan.Reasons)
	}
	if hasStage(plan.Stages, StageFinalBurial) {
		t.Fatal("unburied cable must not schedule a burial stage")
	}
	for _, op := range plan.Operations {
		if op == string(model.OpBurial) {
			t.Fatal("burial must not be requested from the permit authority")
		}
	}
}

func TestBuildRejectsVesselWithoutDepthCapability(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 63)
	plan, err := Build(cfg, baseRequest(t, ix, res, testAssets(500, 50, 100)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Feasible {
		t.Fatal("expected the plan to be infeasible")
	}
	if !contains(plan.Reasons, ReasonDepthCapability) {
		t.Fatalf("reasons = %v, want %s", plan.Reasons, ReasonDepthCapability)
	}
}

func TestBuildReportsSpareShortfall(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	assets := model.NewAssetSet(model.AssetsDocument{
		Version: model.AssetsSchemaVersion,
		Vessels: []model.Vessel{{
			ID: "CS-ONE", Name: "One", StationDepotID: "DEP-A", TransitSpeedKn: 12, MobilizationHours: 12,
			SpareCableKm: 0.5, SpareJoints: 6, HasROV: true, HasAUV: true, MaxWorkingDepthM: 3000,
			MaxSeaStateM: 2.5, DayRateUnits: 100, AvailableFrom: model.MustParseUTC("2026-03-10T00:00:00Z"),
		}},
		Depots: []model.Depot{{
			ID: "DEP-A", Name: "Alpha", Jurisdiction: "JA", SpareCableKm: 0.5, SpareJoints: 20,
			Access: []model.DepotAccess{{SystemID: "SYS-T", ReferenceKP: 0, DistanceNm: 30}},
		}},
	})
	plan, err := Build(cfg, baseRequest(t, ix, res, assets))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Feasible {
		t.Fatal("expected the plan to be infeasible")
	}
	if !contains(plan.Reasons, ReasonSpareCable) {
		t.Fatalf("reasons = %v, want %s", plan.Reasons, ReasonSpareCable)
	}
}

func TestBuildDefersWorkUntilPermitOpens(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	req := baseRequest(t, ix, res, testAssets(3000, 50, 100))
	req.Permits = testPermits("2026-03-14T00:00:00Z", "2026-04-01T00:00:00Z")
	plan, err := Build(cfg, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !plan.Feasible {
		t.Fatalf("expected a feasible plan, reasons %v", plan.Reasons)
	}
	if plan.Window.Start.Before(model.MustParseUTC("2026-03-14T00:00:00Z")) {
		t.Fatalf("window start = %s, want it inside the permit validity", plan.Window.Start)
	}
	if !hasStage(plan.Stages, StageStandby) {
		t.Fatal("expected a standby stage while waiting for the permit")
	}
}

func TestBuildFailsWithoutAnyPermit(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	req := baseRequest(t, ix, res, testAssets(3000, 50, 100))
	req.Permits = nil
	plan, err := Build(cfg, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.Feasible {
		t.Fatal("expected the plan to be infeasible without a permit")
	}
	if !contains(plan.Reasons, ReasonNoPermitWindow) {
		t.Fatalf("reasons = %v, want %s", plan.Reasons, ReasonNoPermitWindow)
	}
}

func TestBuildPicksTheCheaperVesselDeterministically(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 63)
	doc := model.AssetsDocument{
		Version: model.AssetsSchemaVersion,
		Depots: []model.Depot{{
			ID: "DEP-A", Name: "Alpha", Jurisdiction: "JA", SpareCableKm: 200, SpareJoints: 20,
			Access: []model.DepotAccess{{SystemID: "SYS-T", ReferenceKP: 0, DistanceNm: 30}},
		}},
		Vessels: []model.Vessel{
			{
				ID: "CS-EXPENSIVE", Name: "Expensive", StationDepotID: "DEP-A", TransitSpeedKn: 12,
				MobilizationHours: 12, SpareCableKm: 50, SpareJoints: 6, HasROV: true, HasAUV: true,
				MaxWorkingDepthM: 3000, MaxSeaStateM: 2.5, DayRateUnits: 400,
				AvailableFrom: model.MustParseUTC("2026-03-10T00:00:00Z"),
			},
			{
				ID: "CS-CHEAP", Name: "Cheap", StationDepotID: "DEP-A", TransitSpeedKn: 12,
				MobilizationHours: 12, SpareCableKm: 50, SpareJoints: 6, HasROV: true, HasAUV: true,
				MaxWorkingDepthM: 3000, MaxSeaStateM: 2.5, DayRateUnits: 90,
				AvailableFrom: model.MustParseUTC("2026-03-10T00:00:00Z"),
			},
		},
	}
	plan, err := Build(cfg, baseRequest(t, ix, res, model.NewAssetSet(doc)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.VesselID != "CS-CHEAP" {
		t.Fatalf("selected %q, want the cheaper vessel", plan.VesselID)
	}
	if len(plan.Candidates) != 2 {
		t.Fatalf("candidates = %d, want one per vessel and depot pairing", len(plan.Candidates))
	}
	if plan.Candidates[0].VesselID != "CS-CHEAP" {
		t.Fatalf("candidates are not cost ordered: %v", plan.Candidates[0].VesselID)
	}
}

func TestBuildHonoursVesselAvailabilityOverride(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 63)
	req := baseRequest(t, ix, res, testAssets(3000, 50, 100))
	req.VesselAvailability = map[string]model.UTCTime{"CS-ONE": model.MustParseUTC("2026-03-13T00:00:00Z")}
	plan, err := Build(cfg, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.StartAt.Before(model.MustParseUTC("2026-03-13T00:00:00Z")) {
		t.Fatalf("plan start = %s, want it after the release time", plan.StartAt)
	}
}

func TestSpareUsageAccountsForSlackBightAndExcess(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	plan, err := Build(cfg, baseRequest(t, ix, res, testAssets(3000, 50, 100)))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := plan.Spares
	wantRoute := cfg.Round(res.WindowWidthKm + 2*cfg.Repair.ExcessCableKm)
	if s.ReplacedRouteKm != wantRoute {
		t.Fatalf("replaced route = %v, want %v", s.ReplacedRouteKm, wantRoute)
	}
	if s.BaseCableKm <= s.ReplacedRouteKm {
		t.Fatal("slack must make the cable longer than the route")
	}
	if s.BightAllowanceKm <= 0 {
		t.Fatal("a known depth must produce a bight allowance")
	}
	if s.TotalCableKm < s.BaseCableKm+s.BightAllowanceKm {
		t.Fatalf("total cable %v is below its components", s.TotalCableKm)
	}
	if s.Joints != cfg.Repair.JointsPerRepair {
		t.Fatalf("joints = %d", s.Joints)
	}
	if !s.Sufficient || s.ShortfallKm != 0 {
		t.Fatalf("expected the stock to be sufficient: %+v", s)
	}
}

func TestBuildFlagsRiskAndDeadline(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	req := baseRequest(t, ix, res, testAssets(3000, 50, 100))
	req.Fault.RestoreBy = model.MustParseUTC("2026-03-10T12:00:00Z")
	plan, err := Build(cfg, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if plan.DeadlineMet {
		t.Fatal("expected the deadline to be missed")
	}
	if !contains(plan.RiskFlags, RiskDeadlineMissed) {
		t.Fatalf("risk flags = %v", plan.RiskFlags)
	}
	if !contains(plan.RiskFlags, RiskAnchoringZone) {
		t.Fatalf("risk flags = %v, want the anchoring zone flag", plan.RiskFlags)
	}
	if plan.RiskScore <= 0 {
		t.Fatalf("risk score = %v", plan.RiskScore)
	}
}

func TestBuildRequiresAsOf(t *testing.T) {
	cfg := config.Default()
	ix := testIndex()
	res := localization(t, ix, 21)
	req := baseRequest(t, ix, res, testAssets(3000, 50, 100))
	req.AsOf = model.UTCTime{}
	if _, err := Build(cfg, req); err == nil {
		t.Fatal("expected an error without an as-of instant")
	}
}

func hasStage(stages []Stage, name string) bool {
	for _, s := range stages {
		if s.Name == name {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
