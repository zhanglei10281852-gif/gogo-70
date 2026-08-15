package planner

import (
	"fmt"
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
	"CableMend/internal/permits"
	"CableMend/internal/weather"
)

// DefaultScheduleAttempts bounds the permit/weather retry loop.
const DefaultScheduleAttempts = 12

// Request carries everything needed to plan one repair.
type Request struct {
	Fault               model.FaultRecord
	Localization        locate.Result
	Index               *model.SystemIndex
	Assets              *model.AssetSet
	Observations        []model.Observation
	Permits             []model.Permit
	AsOf                model.UTCTime
	VesselAvailability  map[string]model.UTCTime
	VesselPositions     map[string]model.Site
	MaxScheduleAttempts int
}

// Build evaluates every vessel and depot pairing and returns the plan for the
// cheapest feasible one.
func Build(cfg config.Config, req Request) (Plan, error) {
	if req.Index == nil {
		return Plan{}, fmt.Errorf("planner requires an indexed system")
	}
	if req.Assets == nil {
		return Plan{}, fmt.Errorf("planner requires an asset set")
	}
	res := req.Localization
	if res.SystemID != req.Index.ID() {
		return Plan{}, fmt.Errorf("localization is for system %s but index is for %s", res.SystemID, req.Index.ID())
	}
	if req.AsOf.IsZero() {
		return Plan{}, fmt.Errorf("planner requires an as-of instant")
	}
	attempts := req.MaxScheduleAttempts
	if attempts <= 0 {
		attempts = DefaultScheduleAttempts
	}

	site := model.Site{SystemID: res.SystemID, KP: res.BestKP}
	joints := cfg.Repair.JointsPerRepair
	burialKm := res.BuriedLengthKm
	onSiteHours := cfg.OnSiteHours(burialKm, joints)
	ops := operationsFor(burialKm)
	zones := req.Index.ZonesIn(res.WindowLowKP, res.WindowHighKP)
	need := computeSpares(cfg, res)

	plan := Plan{
		FaultID:        faultID(req),
		SystemID:       res.SystemID,
		SystemName:     res.SystemName,
		AsOf:           req.AsOf,
		Site:           site,
		Localization:   briefOf(res),
		Spares:         need,
		OnSiteHours:    cfg.Round(onSiteHours),
		Operations:     model.OperationNames(ops),
		SegmentID:      res.SegmentID,
		BurialLengthKm: cfg.Round(burialKm),
		RestoreBy:      req.Fault.RestoreBy,
		DeadlineMet:    true,
	}

	var candidates []Candidate
	for _, vessel := range req.Assets.Vessels() {
		for _, depot := range req.Assets.Depots() {
			cand := evaluate(cfg, req, vessel, depot, site, zones, ops, need, onSiteHours, attempts)
			candidates = append(candidates, cand)
		}
	}
	sortCandidates(candidates)
	plan.Candidates = candidates

	best, ok := firstFeasible(candidates)
	if !ok {
		plan.Feasible = false
		plan.Reasons = collectReasons(candidates)
		plan.RiskFlags = riskFlags(cfg, req, res, need, Candidate{}, false)
		plan.RiskScore = riskScore(cfg, req, res, need, Candidate{}, false)
		plan.Notes = append(plan.Notes, "no vessel and depot pairing satisfies the constraints")
		sort.Strings(plan.Notes)
		return plan, nil
	}

	plan.Feasible = true
	plan.VesselID = best.VesselID
	plan.VesselName = best.VesselName
	plan.DepotID = best.DepotID
	plan.DepotName = best.DepotName
	plan.Cost = best.Cost
	plan.CostItems = best.CostItems
	plan.Window = best.Window
	plan.Limits = weather.LimitsFor(cfg, vesselSeaState(req.Assets, best.VesselID))
	plan.Permits = best.Permits
	plan.Spares = best.Spares
	plan.Stages = buildStages(cfg, best, burialKm, joints)
	if len(plan.Stages) > 0 {
		plan.StartAt = plan.Stages[0].StartAt
		plan.EndAt = plan.Stages[len(plan.Stages)-1].EndAt
	}
	plan.TotalHours = best.TotalHours
	plan.ReleaseAt = best.CompletesAt
	plan.ReleaseSite = site
	if !req.Fault.RestoreBy.IsZero() {
		plan.DeadlineMet = !plan.EndAt.After(req.Fault.RestoreBy)
	}
	plan.RiskFlags = riskFlags(cfg, req, res, best.Spares, best, plan.DeadlineMet)
	plan.RiskScore = riskScore(cfg, req, res, best.Spares, best, plan.DeadlineMet)
	plan.Notes = notesFor(cfg, best, burialKm)
	return plan, nil
}

// evaluate scores one vessel and depot pairing.
func evaluate(cfg config.Config, req Request, vessel model.Vessel, depot model.Depot, site model.Site,
	zones []model.ProtectionZone, ops []model.Operation, need SpareUsage, onSiteHours float64, maxAttempts int) Candidate {
	cand := Candidate{
		VesselID:          vessel.ID,
		VesselName:        vessel.Name,
		DepotID:           depot.ID,
		DepotName:         depot.Name,
		MobilizationHours: cfg.Round(vessel.MobilizationHours),
		DemobHours:        cfg.Round(cfg.Repair.DemobilizationHours),
		OnSiteHours:       cfg.Round(onSiteHours),
		Spares:            need,
	}
	var reasons []string

	depotToSite, err := depot.DistanceNmToSite(site)
	if err != nil {
		cand.Reasons = []string{ReasonNoDepotAccess}
		return cand
	}
	toDepot, originKind, originID, err := originLeg(req, vessel, depot)
	if err != nil {
		cand.Reasons = []string{ReasonRepositionUnknown}
		return cand
	}
	cand.OriginKind = originKind
	cand.OriginID = originID
	cand.ToDepotNm = cfg.Round(toDepot)
	cand.DepotToSiteNm = cfg.Round(depotToSite)
	transitNm := toDepot + depotToSite
	cand.TransitNm = cfg.Round(transitNm)
	transitHours, err := numeric.TransitHours(transitNm, vessel.TransitSpeedKn)
	if err != nil {
		cand.Reasons = []string{ReasonRepositionUnknown}
		return cand
	}
	cand.TransitHours = cfg.Round(transitHours)

	res := req.Localization
	if res.Depth.Known && res.Depth.DepthM+cfg.Repair.DepthMarginM > vessel.MaxWorkingDepthM {
		reasons = append(reasons, ReasonDepthCapability)
	}
	if !vessel.HasROV && !vessel.HasAUV {
		reasons = append(reasons, ReasonNoSubseaVehicle)
	}
	if res.BuriedLengthKm > 0 && !vessel.HasROV {
		reasons = append(reasons, ReasonROVRequired)
	}
	cand.Spares = allocateSpares(cfg, need, vessel, depot)
	if cand.Spares.ShortfallKm > 0 {
		reasons = append(reasons, ReasonSpareCable)
	}
	if cand.Spares.JointShortfall > 0 {
		reasons = append(reasons, ReasonSpareJoints)
	}
	if len(reasons) > 0 {
		sort.Strings(reasons)
		cand.Reasons = reasons
		return cand
	}

	readyAt := model.MaxTime(req.AsOf, vessel.AvailableFrom)
	if override, ok := req.VesselAvailability[vessel.ID]; ok {
		readyAt = model.MaxTime(readyAt, override)
	}
	cand.ReadyAt = readyAt
	onSiteReady := readyAt.AddHours(vessel.MobilizationHours + transitHours)
	cand.OnSiteFrom = onSiteReady

	limits := weather.LimitsFor(cfg, vessel.MaxSeaStateM)
	sel, assess, attempts, err := schedule(cfg, req, zones, ops, limits, onSiteHours, onSiteReady, maxAttempts)
	cand.Attempts = attempts
	cand.Permits = assess
	if err != nil {
		cand.Reasons = []string{err.Error()}
		return cand
	}
	if !sel.Found {
		cand.Reasons = []string{ReasonNoWeatherWindow}
		return cand
	}
	if !assess.Satisfied {
		cand.Reasons = []string{ReasonNoPermitWindow}
		return cand
	}

	cand.Window = sel.Window
	cand.WindowFound = true
	standby := sel.Window.Start.HoursSince(onSiteReady)
	if standby < 0 {
		standby = 0
	}
	cand.StandbyHours = cfg.Round(standby)
	completes := sel.Window.End.AddHours(cfg.Repair.DemobilizationHours)
	cand.CompletesAt = completes
	cand.TotalHours = cfg.Round(completes.HoursSince(readyAt))
	cand.CostItems, cand.Cost = costOf(cfg, vessel, cand)
	cand.Feasible = true
	return cand
}

// originLeg returns the distance from the vessel's current position to the
// loading depot along with a description of that position.
func originLeg(req Request, vessel model.Vessel, depot model.Depot) (float64, string, string, error) {
	if pos, ok := req.VesselPositions[vessel.ID]; ok && pos.SystemID != "" {
		dist, err := depot.DistanceNmToSite(pos)
		if err != nil {
			return 0, "", "", err
		}
		return dist, "site", pos.String(), nil
	}
	station := vessel.StationDepotID
	if station == "" {
		station = vessel.HomeDepotID
	}
	if station == "" {
		return 0, "", "", fmt.Errorf("vessel %s has no station depot", vessel.ID)
	}
	dist, _, err := req.Assets.DepotDistanceNm(station, depot.ID)
	if err != nil {
		return 0, "", "", err
	}
	return dist, "depot", station, nil
}

// schedule finds the earliest weather window that a permit authorizes.
func schedule(cfg config.Config, req Request, zones []model.ProtectionZone, ops []model.Operation,
	limits model.Limits, onSiteHours float64, notBefore model.UTCTime, maxAttempts int) (weather.Selection, permits.Assessment, int, error) {
	starts := permits.CandidateStarts(req.Localization.SystemID, zones, req.Permits, notBefore)
	if len(starts) == 0 {
		starts = []model.UTCTime{notBefore}
	}
	var lastAssess permits.Assessment
	var lastSel weather.Selection
	attempts := 0
	for i := 0; i < len(starts) && attempts < maxAttempts; i++ {
		attempts++
		analysis, err := weather.Analyze(cfg, req.Localization.SystemID, req.Observations, limits, onSiteHours, starts[i], false)
		if err != nil {
			return weather.Selection{}, permits.Assessment{}, attempts, err
		}
		lastSel = analysis.Selection
		if !analysis.Selection.Found {
			return analysis.Selection, lastAssess, attempts, nil
		}
		window := analysis.Selection.Window.Interval()
		assess := permits.Assess(req.Localization.SystemID, zones, req.Permits, window, ops)
		lastAssess = assess
		if assess.Satisfied {
			return analysis.Selection, assess, attempts, nil
		}
		for i+1 < len(starts) && !starts[i+1].After(analysis.Selection.Window.Start) {
			i++
		}
	}
	return lastSel, lastAssess, attempts, nil
}

// buildStages expands the selected candidate into the ordered stage plan.
func buildStages(cfg config.Config, cand Candidate, burialKm float64, joints int) []Stage {
	var stages []Stage
	cursor := cand.ReadyAt
	cumulative := 0.0
	add := func(name, operation string, hours float64, onSite bool, note string) {
		if hours <= 0 {
			return
		}
		start := cursor
		end := start.AddHours(hours)
		cumulative += hours
		stages = append(stages, Stage{
			Order:           len(stages) + 1,
			Name:            name,
			Operation:       operation,
			StartAt:         start,
			EndAt:           end,
			Hours:           cfg.Round(hours),
			CumulativeHours: cfg.Round(cumulative),
			OnSite:          onSite,
			Note:            note,
		})
		cursor = end
	}
	add(StageMobilize, "", cand.MobilizationHours, false, "load spares and jointing consumables at "+cand.DepotID)
	add(StageTransit, "", cand.TransitHours, false, fmt.Sprintf("%s nm from %s via depot", numeric.Format(cand.TransitNm, cfg.Decimals()), cand.OriginID))
	add(StageStandby, "", cand.StandbyHours, false, "waiting for the first workable window")
	// On-site stages consume exactly the selected window.
	add(StageSurvey, string(model.OpSurvey), cfg.Repair.SurveyHours, true, "confirm the fault position with the subsea vehicle")
	add(StageCutAndHold, string(model.OpCutAndHold), cfg.Repair.CutAndHoldHours, true, "recover and buoy off the faulted cable")
	add(StageSplice, string(model.OpSplice), cfg.Repair.SpliceHoursPerJoint*float64(joints), true, fmt.Sprintf("%d final joints", joints))
	add(StageTest, string(model.OpTest), cfg.Repair.TestHours, true, "end-to-end optical and electrical acceptance")
	if burialKm > 0 {
		add(StageFinalBurial, string(model.OpBurial), cfg.Repair.BurialHoursPerKm*burialKm, true,
			fmt.Sprintf("re-bury %s km", numeric.Format(burialKm, cfg.Decimals())))
	}
	add(StageDemobilize, "", cfg.Repair.DemobilizationHours, false, "release the vessel and close the work order")
	return stages
}

// costOf builds the deterministic selection cost.
func costOf(cfg config.Config, vessel model.Vessel, cand Candidate) ([]CostItem, float64) {
	items := []CostItem{
		{
			Name:   "vessel_time",
			Amount: numeric.Round(cfg.Costs.DayRateWeight*vessel.DayRateUnits*cand.TotalHours/24.0, 6),
			Detail: fmt.Sprintf("%s h at day rate %s", numeric.Format(cand.TotalHours, 3), numeric.Format(vessel.DayRateUnits, 3)),
		},
		{
			Name:   "transit",
			Amount: numeric.Round(cfg.Costs.TransitHourWeight*cand.TransitHours, 6),
			Detail: fmt.Sprintf("%s nm", numeric.Format(cand.TransitNm, 3)),
		},
		{
			Name:   "mobilization",
			Amount: numeric.Round(cfg.Costs.MobilizationHourWeight*cand.MobilizationHours, 6),
		},
		{
			Name:   "standby",
			Amount: numeric.Round(cfg.Costs.StandbyHourWeight*cand.StandbyHours, 6),
		},
		{
			Name:   "depot_transfer",
			Amount: numeric.Round(cfg.Costs.DepotTransferPerKm*numeric.NmToKm(cand.ToDepotNm), 6),
			Detail: fmt.Sprintf("%s nm to the loading depot", numeric.Format(cand.ToDepotNm, 3)),
		},
	}
	if cand.Spares.ShortfallKm > 0 {
		items = append(items, CostItem{
			Name:   "spare_shortfall",
			Amount: numeric.Round(cfg.Costs.SpareShortfallPerKm*cand.Spares.ShortfallKm, 6),
		})
	}
	if !vessel.HasAUV {
		items = append(items, CostItem{
			Name:   "extended_survey",
			Amount: numeric.Round(cfg.Costs.MissingCapabilityPenalty*0.1, 6),
			Detail: "no autonomous survey vehicle embarked",
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	amounts := make([]float64, 0, len(items))
	for _, it := range items {
		amounts = append(amounts, it.Amount)
	}
	return items, numeric.Round(numeric.SortedSum(amounts), 6)
}

// sortCandidates orders candidates deterministically: feasible first, then by
// cost, completion time, vessel id and depot id.
func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Feasible != b.Feasible {
			return a.Feasible
		}
		if a.Feasible && b.Feasible {
			if !numeric.AlmostEqual(a.Cost, b.Cost, 1e-9) {
				return a.Cost < b.Cost
			}
			if !a.CompletesAt.Equal(b.CompletesAt) {
				return a.CompletesAt.Before(b.CompletesAt)
			}
		}
		if a.VesselID != b.VesselID {
			return a.VesselID < b.VesselID
		}
		return a.DepotID < b.DepotID
	})
}

func firstFeasible(candidates []Candidate) (Candidate, bool) {
	for _, c := range candidates {
		if c.Feasible {
			return c, true
		}
	}
	return Candidate{}, false
}

// collectReasons de-duplicates the infeasibility reasons across candidates.
func collectReasons(candidates []Candidate) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		for _, r := range c.Reasons {
			if seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}

// operationsFor lists the permit operations a repair needs.
func operationsFor(burialKm float64) []model.Operation {
	ops := []model.Operation{model.OpSurvey, model.OpCutAndHold, model.OpSplice, model.OpTest}
	if burialKm > 0 {
		ops = append(ops, model.OpBurial)
	}
	return ops
}

func faultID(req Request) string {
	if req.Fault.ID != "" {
		return req.Fault.ID
	}
	return req.Localization.FaultID
}

func vesselSeaState(assets *model.AssetSet, vesselID string) float64 {
	if v, ok := assets.Vessel(vesselID); ok {
		return v.MaxSeaStateM
	}
	return 0
}

// notesFor explains the plan choices in plain language.
func notesFor(cfg config.Config, cand Candidate, burialKm float64) []string {
	notes := []string{
		fmt.Sprintf("vessel %s loads at depot %s before sailing %s nm to the work site",
			cand.VesselID, cand.DepotID, numeric.Format(cand.TransitNm, cfg.Decimals())),
	}
	if cand.StandbyHours > 0 {
		notes = append(notes, fmt.Sprintf("%s h of standby before the workable window opens",
			numeric.Format(cand.StandbyHours, cfg.Decimals())))
	}
	if burialKm <= 0 {
		notes = append(notes, "no burial required in the localization window; the final burial stage is omitted")
	}
	if cand.Spares.FromDepotKm > 0 {
		notes = append(notes, fmt.Sprintf("%s km of spare cable drawn from the depot",
			numeric.Format(cand.Spares.FromDepotKm, cfg.Decimals())))
	}
	sort.Strings(notes)
	return notes
}
