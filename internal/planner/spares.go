package planner

import (
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
)

// computeSpares derives the cable and joint demand of one repair from the
// localization window. The replaced stretch spans the uncertainty window plus an
// excess allowance at each side; the cable actually paid out is longer because of
// route slack, the bight needed to lift the cable to the deck, and a working
// slack allowance.
func computeSpares(cfg config.Config, res locate.Result) SpareUsage {
	slack := res.SegmentSlack
	if slack < 1.0 {
		slack = cfg.Localization.DefaultSlackFactor
	}
	replacedRouteKm := res.WindowWidthKm + 2*cfg.Repair.ExcessCableKm
	baseCableKm := replacedRouteKm * slack
	bightKm := 0.0
	if res.Depth.Known {
		bightKm = cfg.Repair.BightFactor * res.Depth.DepthM / 1000.0
	}
	slackAllowanceKm := cfg.Repair.SpareSlackRatio * baseCableKm
	total := baseCableKm + bightKm + slackAllowanceKm
	return SpareUsage{
		ReplacedRouteKm:  cfg.Round(replacedRouteKm),
		SegmentSlack:     numeric.Round(slack, 6),
		BaseCableKm:      cfg.Round(baseCableKm),
		BightAllowanceKm: cfg.Round(bightKm),
		SlackAllowanceKm: cfg.Round(slackAllowanceKm),
		TotalCableKm:     cfg.Round(total),
		Joints:           cfg.Repair.JointsPerRepair,
	}
}

// allocateSpares splits the demand between the vessel's own stock and the depot,
// reporting any shortfall.
func allocateSpares(cfg config.Config, need SpareUsage, vessel model.Vessel, depot model.Depot) SpareUsage {
	out := need
	remaining := need.TotalCableKm
	fromVessel := remaining
	if vessel.SpareCableKm < fromVessel {
		fromVessel = vessel.SpareCableKm
	}
	remaining -= fromVessel
	fromDepot := remaining
	if depot.SpareCableKm < fromDepot {
		fromDepot = depot.SpareCableKm
	}
	remaining -= fromDepot
	if remaining < 1e-9 {
		remaining = 0
	}
	out.FromVesselKm = cfg.Round(fromVessel)
	out.FromDepotKm = cfg.Round(fromDepot)
	out.ShortfallKm = cfg.Round(remaining)

	jointsRemaining := need.Joints
	jointsFromVessel := jointsRemaining
	if vessel.SpareJoints < jointsFromVessel {
		jointsFromVessel = vessel.SpareJoints
	}
	jointsRemaining -= jointsFromVessel
	jointsFromDepot := jointsRemaining
	if depot.SpareJoints < jointsFromDepot {
		jointsFromDepot = depot.SpareJoints
	}
	jointsRemaining -= jointsFromDepot
	out.JointsFromVessel = jointsFromVessel
	out.JointsFromDepot = jointsFromDepot
	out.JointShortfall = jointsRemaining
	out.Sufficient = out.ShortfallKm == 0 && out.JointShortfall == 0
	return out
}

// riskFlags lists the qualitative risks attached to a plan.
func riskFlags(cfg config.Config, req Request, res locate.Result, spares SpareUsage, cand Candidate, deadlineMet bool) []string {
	seen := map[string]bool{}
	add := func(flag string) { seen[flag] = true }
	if !res.Consistent {
		add(RiskInconsistent)
	}
	if !res.Depth.Known {
		add(RiskNoDepth)
	} else if res.Depth.DepthM >= cfg.Verification.RiskDepthThresholdM {
		add(RiskDeepWater)
	}
	if spares.ShortfallKm > 0 || spares.JointShortfall > 0 {
		add(RiskSpareShortfall)
	}
	if res.WindowWidthKm >= 0.5*cfg.Localization.MaxWindowKm {
		add(RiskWideWindow)
	}
	for _, zone := range res.Zones {
		if zone.AnchoringRestricted {
			add(RiskAnchoringZone)
		}
	}
	if req.Index != nil && res.SegmentID != "" {
		existing := req.Index.JointsInSegment(res.SegmentID)
		if existing+cfg.Repair.JointsPerRepair > cfg.Verification.MaxJointsPerSegment {
			add(RiskJointLimit)
		}
	}
	if !deadlineMet {
		add(RiskDeadlineMissed)
	}
	if req.Fault.SingleRoute || (req.Index != nil && req.Index.System().SingleRoute) {
		add(RiskSingleRouteSystem)
	}
	if cand.WindowFound {
		for _, requirement := range cand.Permits.Requirements {
			for _, status := range requirement.Candidates {
				if status.PermitID != requirement.CoveringPermitID {
					continue
				}
				margin := status.ValidTo.HoursSince(cand.Window.End)
				if margin >= 0 && margin < cfg.Repair.MinWorkHours {
					add(RiskPermitTailNarrow)
				}
			}
		}
	}
	if cand.Feasible && spares.FromDepotKm > 0 {
		if depot, ok := req.Assets.Depot(cand.DepotID); ok && depot.SpareCableKm-spares.FromDepotKm < spares.TotalCableKm {
			add(RiskNoSpareAtDepot)
		}
	}
	out := make([]string, 0, len(seen))
	for flag := range seen {
		out = append(out, flag)
	}
	sort.Strings(out)
	return out
}

// riskScore turns the risk picture into a 0..100 score. The weights are fixed so
// that the score is comparable across campaigns.
func riskScore(cfg config.Config, req Request, res locate.Result, spares SpareUsage, cand Candidate, deadlineMet bool) float64 {
	score := 0.0
	if !res.Consistent {
		score += 20
	}
	if !res.Depth.Known {
		score += 10
	} else {
		score += numeric.Clamp(15*res.Depth.DepthM/cfg.Verification.RiskDepthThresholdM, 0, 15)
	}
	score += numeric.Clamp(15*res.WindowWidthKm/cfg.Localization.MaxWindowKm, 0, 15)
	if spares.ShortfallKm > 0 || spares.JointShortfall > 0 {
		score += 15
	}
	for _, zone := range res.Zones {
		if zone.AnchoringRestricted {
			score += 5
			break
		}
	}
	zoneRisk := 0.0
	for _, zone := range res.Zones {
		if zone.RiskWeight > zoneRisk {
			zoneRisk = zone.RiskWeight
		}
	}
	score += numeric.Clamp(zoneRisk, 0, 10)
	if !deadlineMet {
		score += 15
	}
	if req.Fault.SingleRoute || (req.Index != nil && req.Index.System().SingleRoute) {
		score += 5
	}
	if cand.StandbyHours > 24 {
		score += 5
	}
	return numeric.Round(numeric.Clamp(score, 0, 100), 3)
}
