// Package quality performs post-repair verification: the measured segment loss
// is compared against the loss budget implied by the cable geometry and joint
// count, the joint count is checked against the per-segment limit, achieved
// burial is compared with the design profile, and a residual risk score is
// produced for the repaired segment.
package quality

import (
	"fmt"
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
)

// Finding names.
const (
	FindingLossHigh       = "measured_loss_above_budget"
	FindingLossLow        = "measured_loss_below_budget"
	FindingJointLimit     = "joint_count_exceeds_limit"
	FindingJointAtLimit   = "joint_count_at_limit"
	FindingBurialDeficit  = "burial_below_design"
	FindingNoBurialData   = "no_burial_design_data"
	FindingNoROV          = "no_rov_inspection_recorded"
	FindingUnknownSegment = "segment_not_found_in_system"
	FindingDeepWater      = "deep_water_segment"
)

// Risk bands.
const (
	BandLow      = "low"
	BandModerate = "moderate"
	BandHigh     = "high"
	BandSevere   = "severe"
)

// SegmentCheck is the verification outcome for one record.
type SegmentCheck struct {
	RecordID            string        `json:"record_id"`
	SystemID            string        `json:"system_id"`
	SegmentID           string        `json:"segment_id"`
	FaultID             string        `json:"fault_id"`
	MeasuredAt          model.UTCTime `json:"measured_at"`
	RepairKP            float64       `json:"repair_kp"`
	MeasuredLossDb      float64       `json:"measured_loss_db"`
	ExpectedLossDb      float64       `json:"expected_loss_db"`
	DeltaDb             float64       `json:"delta_db"`
	ToleranceDb         float64       `json:"tolerance_db"`
	LossWithinTolerance bool          `json:"loss_within_tolerance"`
	JointsAfterRepair   int           `json:"joints_after_repair"`
	MaxJoints           int           `json:"max_joints"`
	JointsWithinLimit   bool          `json:"joints_within_limit"`
	BurialDesignM       float64       `json:"burial_design_m"`
	BurialAchievedM     float64       `json:"burial_achieved_m"`
	BurialDeficitM      float64       `json:"burial_deficit_m"`
	BurialAcceptable    bool          `json:"burial_acceptable"`
	DepthM              float64       `json:"depth_m"`
	SpareCableUsedKm    float64       `json:"spare_cable_used_km"`
	ROVInspection       bool          `json:"rov_inspection"`
	ResidualRisk        float64       `json:"residual_risk"`
	RiskBand            string        `json:"risk_band"`
	Findings            []string      `json:"findings"`
	Accepted            bool          `json:"accepted"`
}

// FindingCount aggregates findings across the report.
type FindingCount struct {
	Finding string `json:"finding"`
	Count   int    `json:"count"`
}

// Report is the full verification outcome.
type Report struct {
	RecordCount      int            `json:"record_count"`
	SystemCount      int            `json:"system_count"`
	AcceptedCount    int            `json:"accepted_count"`
	RejectedCount    int            `json:"rejected_count"`
	MeanResidualRisk float64        `json:"mean_residual_risk"`
	WorstRecordID    string         `json:"worst_record_id,omitempty"`
	WorstResidual    float64        `json:"worst_residual_risk"`
	Findings         []FindingCount `json:"findings"`
	Checks           []SegmentCheck `json:"checks"`
	MeasuredFrom     model.UTCTime  `json:"measured_from"`
	MeasuredTo       model.UTCTime  `json:"measured_to"`
}

// Verify evaluates every verification record against its cable system.
func Verify(cfg config.Config, set *model.SystemSet, records []model.VerificationRecord) (Report, error) {
	if set == nil {
		return Report{}, fmt.Errorf("verification requires indexed systems")
	}
	if len(records) == 0 {
		return Report{}, fmt.Errorf("verification requires at least one record")
	}
	sorted := model.SortVerifications(records)
	report := Report{RecordCount: len(sorted)}
	systems := map[string]bool{}
	findings := map[string]int{}
	risks := make([]float64, 0, len(sorted))
	var times []model.UTCTime

	for _, rec := range sorted {
		ix, err := set.MustGet(rec.SystemID)
		if err != nil {
			return Report{}, err
		}
		systems[rec.SystemID] = true
		times = append(times, rec.MeasuredAt)
		check := evaluate(cfg, ix, rec)
		for _, f := range check.Findings {
			findings[f]++
		}
		risks = append(risks, check.ResidualRisk)
		if check.Accepted {
			report.AcceptedCount++
		} else {
			report.RejectedCount++
		}
		if check.ResidualRisk > report.WorstResidual || report.WorstRecordID == "" {
			report.WorstResidual = check.ResidualRisk
			report.WorstRecordID = check.RecordID
		}
		report.Checks = append(report.Checks, check)
	}

	report.SystemCount = len(systems)
	report.MeanResidualRisk = cfg.Round(numeric.Mean(risks))
	report.MeasuredFrom = model.EarliestTime(times)
	report.MeasuredTo = model.LatestTime(times)
	keys := make([]string, 0, len(findings))
	for k := range findings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		report.Findings = append(report.Findings, FindingCount{Finding: k, Count: findings[k]})
	}
	return report, nil
}

// evaluate checks one record.
func evaluate(cfg config.Config, ix *model.SystemIndex, rec model.VerificationRecord) SegmentCheck {
	v := cfg.Verification
	check := SegmentCheck{
		RecordID:          rec.ID,
		SystemID:          rec.SystemID,
		SegmentID:         rec.SegmentID,
		FaultID:           rec.FaultID,
		MeasuredAt:        rec.MeasuredAt,
		RepairKP:          cfg.Round(rec.RepairKP),
		MeasuredLossDb:    cfg.Round(rec.MeasuredLossDb),
		ToleranceDb:       cfg.Round(v.LossToleranceDb),
		JointsAfterRepair: rec.JointsAfterRepair,
		MaxJoints:         v.MaxJointsPerSegment,
		BurialAchievedM:   cfg.Round(rec.BurialAchievedM),
		SpareCableUsedKm:  cfg.Round(rec.SpareCableUsedKm),
		ROVInspection:     rec.ROVInspection,
	}
	var findings []string

	seg, ok := ix.SegmentByID(rec.SegmentID)
	if !ok {
		findings = append(findings, FindingUnknownSegment)
		check.Findings = sortedUnique(findings)
		check.ResidualRisk = 100
		check.RiskBand = BandSevere
		check.Accepted = false
		return check
	}

	expected := ix.ExpectedSegmentLossDb(seg, rec.JointsAfterRepair, v.JointLossDb)
	delta := rec.MeasuredLossDb - expected
	check.ExpectedLossDb = cfg.Round(expected)
	check.DeltaDb = cfg.Round(delta)
	check.LossWithinTolerance = absFloat(delta) <= v.LossToleranceDb+1e-9
	if !check.LossWithinTolerance {
		if delta > 0 {
			findings = append(findings, FindingLossHigh)
		} else {
			findings = append(findings, FindingLossLow)
		}
	}

	check.JointsWithinLimit = rec.JointsAfterRepair <= v.MaxJointsPerSegment
	if !check.JointsWithinLimit {
		findings = append(findings, FindingJointLimit)
	} else if rec.JointsAfterRepair == v.MaxJointsPerSegment {
		findings = append(findings, FindingJointAtLimit)
	}

	depth := ix.DepthAt(rec.RepairKP)
	check.DepthM = cfg.Round(depth.DepthM)
	check.BurialDesignM = cfg.Round(depth.BurialDepthM)
	if !depth.Known {
		findings = append(findings, FindingNoBurialData)
		check.BurialAcceptable = true
	} else {
		deficit := depth.BurialDepthM - rec.BurialAchievedM
		if deficit < 0 {
			deficit = 0
		}
		check.BurialDeficitM = cfg.Round(deficit)
		check.BurialAcceptable = deficit <= v.BurialDeficitToleranceM+1e-9
		if !check.BurialAcceptable {
			findings = append(findings, FindingBurialDeficit)
		}
		if depth.DepthM >= v.RiskDepthThresholdM {
			findings = append(findings, FindingDeepWater)
		}
	}
	if !rec.ROVInspection {
		findings = append(findings, FindingNoROV)
	}

	check.Findings = sortedUnique(findings)
	check.ResidualRisk, check.RiskBand = residualRisk(cfg, ix, seg, rec, check)
	check.Accepted = check.LossWithinTolerance && check.JointsWithinLimit && check.BurialAcceptable
	return check
}

// residualRisk scores the repaired segment from 0 (nominal) to 100 (severe).
func residualRisk(cfg config.Config, ix *model.SystemIndex, seg model.Segment, rec model.VerificationRecord, check SegmentCheck) (float64, string) {
	v := cfg.Verification
	score := 0.0
	// Loss margin: full weight once the deviation reaches twice the tolerance.
	lossRatio := absFloat(check.DeltaDb) / (2 * v.LossToleranceDb)
	score += numeric.Clamp(30*lossRatio, 0, 30)
	// Joint headroom.
	if v.MaxJointsPerSegment > 0 {
		jointRatio := float64(rec.JointsAfterRepair) / float64(v.MaxJointsPerSegment)
		score += numeric.Clamp(20*jointRatio, 0, 20)
	}
	if !check.JointsWithinLimit {
		score += 10
	}
	// Burial deficit against the tolerance band.
	if check.BurialDesignM > 0 {
		deficitRatio := check.BurialDeficitM / (check.BurialDesignM + 1e-9)
		score += numeric.Clamp(20*deficitRatio, 0, 20)
	}
	// Water depth relative to the configured risk threshold.
	if check.DepthM > 0 {
		score += numeric.Clamp(10*check.DepthM/v.RiskDepthThresholdM, 0, 10)
	}
	// Protection zone exposure at the repair position.
	zoneRisk := 0.0
	for _, zone := range ix.ZonesAt(rec.RepairKP) {
		if zone.RiskWeight > zoneRisk {
			zoneRisk = zone.RiskWeight
		}
		if zone.AnchoringRestricted && zoneRisk < 2 {
			zoneRisk = 2
		}
	}
	score += numeric.Clamp(zoneRisk, 0, 10)
	if !rec.ROVInspection {
		score += 5
	}
	if seg.LossDbPerKm <= 0 {
		score += 2
	}
	score = numeric.Clamp(score, 0, 100)
	return numeric.Round(score, 3), band(score)
}

func band(score float64) string {
	switch {
	case score < 20:
		return BandLow
	case score < 45:
		return BandModerate
	case score < 70:
		return BandHigh
	default:
		return BandSevere
	}
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
