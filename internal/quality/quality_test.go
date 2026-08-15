package quality

import (
	"math"
	"testing"

	"CableMend/internal/config"
	"CableMend/internal/model"
)

func systemSet() *model.SystemSet {
	doc := model.SystemsDocument{
		Version: model.SystemsSchemaVersion,
		Systems: []model.CableSystem{{
			ID: "SYS-T", Name: "Test Link", SlackFactor: 1.05,
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
		}},
	}
	return model.NewSystemSet(doc, 1.03)
}

// expectedLoss is the loss budget the implementation should reproduce.
func expectedLoss(joints int) float64 {
	return 100*1.05*0.2 + float64(joints)*0.15
}

func record(id string, loss float64, joints int, kp, burial float64, rov bool) model.VerificationRecord {
	return model.VerificationRecord{
		ID: id, SystemID: "SYS-T", SegmentID: "SEG-1", FaultID: "F-1",
		MeasuredAt: model.MustParseUTC("2026-03-15T00:00:00Z"), MeasuredLossDb: loss,
		JointsAfterRepair: joints, RepairKP: kp, SpareCableUsedKm: 8, BurialAchievedM: burial,
		ROVInspection: rov,
	}
}

func TestVerifyAcceptsMeasurementInsideTolerance(t *testing.T) {
	cfg := config.Default()
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(3)+0.2, 3, 20, 1.2, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if math.Abs(check.ExpectedLossDb-expectedLoss(3)) > 1e-6 {
		t.Fatalf("expected loss = %v, want %v", check.ExpectedLossDb, expectedLoss(3))
	}
	if !check.LossWithinTolerance || !check.Accepted {
		t.Fatalf("check = %+v", check)
	}
	if rep.AcceptedCount != 1 || rep.RejectedCount != 0 {
		t.Fatalf("counts = %d accepted, %d rejected", rep.AcceptedCount, rep.RejectedCount)
	}
	if check.RiskBand == "" {
		t.Fatal("expected a risk band")
	}
}

func TestVerifyRejectsExcessLoss(t *testing.T) {
	cfg := config.Default()
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(3)+2.0, 3, 20, 1.2, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if check.Accepted {
		t.Fatal("expected the record to be rejected")
	}
	if !containsFinding(check.Findings, FindingLossHigh) {
		t.Fatalf("findings = %v", check.Findings)
	}
	if rep.RejectedCount != 1 {
		t.Fatalf("rejected = %d", rep.RejectedCount)
	}
}

func TestVerifyRejectsLossBelowBudget(t *testing.T) {
	cfg := config.Default()
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(3)-2.0, 3, 20, 1.2, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !containsFinding(rep.Checks[0].Findings, FindingLossLow) {
		t.Fatalf("findings = %v", rep.Checks[0].Findings)
	}
}

func TestVerifyEnforcesJointLimit(t *testing.T) {
	cfg := config.Default()
	joints := cfg.Verification.MaxJointsPerSegment + 1
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(joints), joints, 20, 1.2, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if check.JointsWithinLimit || check.Accepted {
		t.Fatalf("check = %+v", check)
	}
	if !containsFinding(check.Findings, FindingJointLimit) {
		t.Fatalf("findings = %v", check.Findings)
	}
}

func TestVerifyFlagsJointCountAtLimit(t *testing.T) {
	cfg := config.Default()
	joints := cfg.Verification.MaxJointsPerSegment
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(joints), joints, 20, 1.2, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !containsFinding(rep.Checks[0].Findings, FindingJointAtLimit) {
		t.Fatalf("findings = %v", rep.Checks[0].Findings)
	}
	if !rep.Checks[0].Accepted {
		t.Fatal("being at the limit should still be accepted")
	}
}

func TestVerifyFlagsBurialDeficit(t *testing.T) {
	cfg := config.Default()
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(3), 3, 20, 0.4, true)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if check.BurialAcceptable || check.Accepted {
		t.Fatalf("check = %+v", check)
	}
	if math.Abs(check.BurialDeficitM-0.8) > 1e-6 {
		t.Fatalf("burial deficit = %v, want 0.8", check.BurialDeficitM)
	}
}

func TestVerifyFlagsDeepWaterAndMissingInspection(t *testing.T) {
	cfg := config.Default()
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{record("VR-1", expectedLoss(3), 3, 60, 0, false)})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if !containsFinding(check.Findings, FindingDeepWater) {
		t.Fatalf("findings = %v, want the deep water flag", check.Findings)
	}
	if !containsFinding(check.Findings, FindingNoROV) {
		t.Fatalf("findings = %v, want the inspection flag", check.Findings)
	}
	if !check.Accepted {
		t.Fatal("deep water alone must not reject the repair")
	}
}

func TestVerifyRejectsUnknownSegment(t *testing.T) {
	cfg := config.Default()
	rec := record("VR-1", 20, 3, 20, 1.2, true)
	rec.SegmentID = "SEG-MISSING"
	rep, err := Verify(cfg, systemSet(), []model.VerificationRecord{rec})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	check := rep.Checks[0]
	if check.Accepted || check.RiskBand != BandSevere {
		t.Fatalf("check = %+v", check)
	}
	if !containsFinding(check.Findings, FindingUnknownSegment) {
		t.Fatalf("findings = %v", check.Findings)
	}
}

func TestVerifyAggregatesFindingsAndWorstRecord(t *testing.T) {
	cfg := config.Default()
	records := []model.VerificationRecord{
		record("VR-1", expectedLoss(3), 3, 60, 0, false),
		record("VR-2", expectedLoss(2), 2, 20, 1.2, true),
	}
	rep, err := Verify(cfg, systemSet(), records)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(rep.Findings) == 0 {
		t.Fatal("expected aggregated findings")
	}
	for i := 1; i < len(rep.Findings); i++ {
		if rep.Findings[i-1].Finding > rep.Findings[i].Finding {
			t.Fatalf("findings are not sorted: %v", rep.Findings)
		}
	}
	if rep.WorstRecordID != "VR-1" {
		t.Fatalf("worst record = %q, want the deep water record without inspection", rep.WorstRecordID)
	}
	if rep.MeanResidualRisk <= 0 {
		t.Fatalf("mean residual risk = %v", rep.MeanResidualRisk)
	}
	if rep.MeasuredFrom.IsZero() || rep.MeasuredTo.IsZero() {
		t.Fatal("expected the measurement span to be reported")
	}
}

func TestVerifyRequiresRecords(t *testing.T) {
	if _, err := Verify(config.Default(), systemSet(), nil); err == nil {
		t.Fatal("expected an error without records")
	}
	if _, err := Verify(config.Default(), nil, []model.VerificationRecord{record("VR-1", 20, 2, 20, 1.2, true)}); err == nil {
		t.Fatal("expected an error without systems")
	}
}

func TestRiskBands(t *testing.T) {
	cases := map[float64]string{5: BandLow, 30: BandModerate, 50: BandHigh, 90: BandSevere}
	for score, want := range cases {
		if got := band(score); got != want {
			t.Fatalf("band(%v) = %q, want %q", score, got, want)
		}
	}
}

func containsFinding(findings []string, want string) bool {
	for _, f := range findings {
		if f == want {
			return true
		}
	}
	return false
}
