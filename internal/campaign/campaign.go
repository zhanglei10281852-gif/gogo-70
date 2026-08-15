// Package campaign plans repairs for several faults at once. Faults are ordered
// by a priority score derived from traffic impact and the declared urgency,
// vessels are held until they are released by the previous repair, and depot and
// vessel stock is consumed as the campaign progresses.
package campaign

import (
	"fmt"
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
	"CableMend/internal/planner"
)

// PriorityScore records how a fault's priority was derived.
type PriorityScore struct {
	FaultID          string        `json:"fault_id"`
	SystemID         string        `json:"system_id"`
	DeclaredPriority float64       `json:"declared_priority"`
	TrafficTbps      float64       `json:"traffic_tbps"`
	SingleRoute      bool          `json:"single_route"`
	ReportedAt       model.UTCTime `json:"reported_at"`
	Score            float64       `json:"score"`
	Rank             int           `json:"rank"`
}

// Assignment is one scheduled repair inside the campaign.
type Assignment struct {
	Order        int                `json:"order"`
	FaultID      string             `json:"fault_id"`
	SystemID     string             `json:"system_id"`
	SystemName   string             `json:"system_name"`
	PriorityRank int                `json:"priority_rank"`
	Priority     float64            `json:"priority_score"`
	VesselID     string             `json:"vessel_id"`
	DepotID      string             `json:"depot_id"`
	StartAt      model.UTCTime      `json:"start_at"`
	EndAt        model.UTCTime      `json:"end_at"`
	ReleaseAt    model.UTCTime      `json:"release_at"`
	TotalHours   float64            `json:"total_hours"`
	Cost         float64            `json:"cost"`
	RiskScore    float64            `json:"risk_score"`
	DeadlineMet  bool               `json:"deadline_met"`
	SpareCableKm float64            `json:"spare_cable_km"`
	Joints       int                `json:"joints"`
	Plan         planner.Plan       `json:"plan"`
	Contention   []ContentionRecord `json:"contention,omitempty"`
}

// ContentionRecord notes that a vessel was already committed when this fault was
// scheduled.
type ContentionRecord struct {
	VesselID    string        `json:"vessel_id"`
	BusyUntil   model.UTCTime `json:"busy_until"`
	FromFaultID string        `json:"from_fault_id"`
}

// Unassigned is a fault the campaign could not schedule.
type Unassigned struct {
	FaultID      string   `json:"fault_id"`
	SystemID     string   `json:"system_id"`
	PriorityRank int      `json:"priority_rank"`
	Priority     float64  `json:"priority_score"`
	Reasons      []string `json:"reasons"`
}

// VesselUtilization summarizes one vessel's campaign workload.
type VesselUtilization struct {
	VesselID    string        `json:"vessel_id"`
	Assignments int           `json:"assignments"`
	BusyHours   float64       `json:"busy_hours"`
	FirstStart  model.UTCTime `json:"first_start"`
	LastRelease model.UTCTime `json:"last_release"`
	FinalSite   model.Site    `json:"final_site"`
	FaultIDs    []string      `json:"fault_ids"`
}

// DepotDraw records how much stock a depot supplied.
type DepotDraw struct {
	DepotID     string  `json:"depot_id"`
	CableKm     float64 `json:"cable_km"`
	Joints      int     `json:"joints"`
	RemainingKm float64 `json:"remaining_km"`
}

// Result is the full campaign plan.
type Result struct {
	AsOf          model.UTCTime       `json:"as_of"`
	FaultCount    int                 `json:"fault_count"`
	Priorities    []PriorityScore     `json:"priorities"`
	Assignments   []Assignment        `json:"assignments"`
	Unassigned    []Unassigned        `json:"unassigned"`
	Vessels       []VesselUtilization `json:"vessels"`
	DepotDraws    []DepotDraw         `json:"depot_draws"`
	TotalCost     float64             `json:"total_cost"`
	TotalCableKm  float64             `json:"total_cable_km"`
	TotalJoints   int                 `json:"total_joints"`
	MakespanHours float64             `json:"makespan_hours"`
	FirstStart    model.UTCTime       `json:"first_start"`
	LastRelease   model.UTCTime       `json:"last_release"`
	Notes         []string            `json:"notes"`
}

// Request carries the campaign inputs.
type Request struct {
	Faults        []model.FaultRecord
	Localizations map[string]locate.Result
	Systems       *model.SystemSet
	Assets        model.AssetsDocument
	Observations  []model.Observation
	Permits       []model.Permit
	AsOf          model.UTCTime
	IncludePlans  bool
}

// Plan schedules every fault in priority order.
func Plan(cfg config.Config, req Request) (Result, error) {
	if req.Systems == nil {
		return Result{}, fmt.Errorf("campaign requires indexed systems")
	}
	if req.AsOf.IsZero() {
		return Result{}, fmt.Errorf("campaign requires an as-of instant")
	}
	faults := model.SortFaults(req.Faults)
	if len(faults) == 0 {
		return Result{}, fmt.Errorf("campaign requires at least one fault")
	}

	scores := scoreFaults(cfg, req, faults)
	order := make([]model.FaultRecord, 0, len(faults))
	byID := map[string]model.FaultRecord{}
	for _, f := range faults {
		byID[f.ID] = f
	}
	for _, s := range scores {
		order = append(order, byID[s.FaultID])
	}
	rankOf := map[string]int{}
	scoreOf := map[string]float64{}
	for _, s := range scores {
		rankOf[s.FaultID] = s.Rank
		scoreOf[s.FaultID] = s.Score
	}

	result := Result{AsOf: req.AsOf, FaultCount: len(faults), Priorities: scores}
	availability := map[string]model.UTCTime{}
	positions := map[string]model.Site{}
	lastFault := map[string]string{}
	busyHours := map[string]float64{}
	firstStart := map[string]model.UTCTime{}
	lastRelease := map[string]model.UTCTime{}
	faultsByVessel := map[string][]string{}
	depotCable := map[string]float64{}
	depotJoints := map[string]int{}
	vesselCable := map[string]float64{}
	vesselJoints := map[string]int{}

	for _, fault := range order {
		res, ok := req.Localizations[fault.ID]
		if !ok {
			result.Unassigned = append(result.Unassigned, Unassigned{
				FaultID:      fault.ID,
				SystemID:     fault.SystemID,
				PriorityRank: rankOf[fault.ID],
				Priority:     scoreOf[fault.ID],
				Reasons:      []string{"no localization available for this fault"},
			})
			continue
		}
		ix, err := req.Systems.MustGet(fault.SystemID)
		if err != nil {
			return Result{}, err
		}
		assets := model.NewAssetSet(applyConsumption(req.Assets, depotCable, depotJoints, vesselCable, vesselJoints))
		plan, err := planner.Build(cfg, planner.Request{
			Fault:              fault,
			Localization:       res,
			Index:              ix,
			Assets:             assets,
			Observations:       req.Observations,
			Permits:            req.Permits,
			AsOf:               req.AsOf,
			VesselAvailability: availability,
			VesselPositions:    positions,
		})
		if err != nil {
			return Result{}, fmt.Errorf("fault %s: %w", fault.ID, err)
		}
		if !plan.Feasible {
			result.Unassigned = append(result.Unassigned, Unassigned{
				FaultID:      fault.ID,
				SystemID:     fault.SystemID,
				PriorityRank: rankOf[fault.ID],
				Priority:     scoreOf[fault.ID],
				Reasons:      plan.Reasons,
			})
			continue
		}
		assignment := Assignment{
			Order:        len(result.Assignments) + 1,
			FaultID:      fault.ID,
			SystemID:     fault.SystemID,
			SystemName:   plan.SystemName,
			PriorityRank: rankOf[fault.ID],
			Priority:     scoreOf[fault.ID],
			VesselID:     plan.VesselID,
			DepotID:      plan.DepotID,
			StartAt:      plan.StartAt,
			EndAt:        plan.EndAt,
			ReleaseAt:    plan.ReleaseAt,
			TotalHours:   plan.TotalHours,
			Cost:         plan.Cost,
			RiskScore:    plan.RiskScore,
			DeadlineMet:  plan.DeadlineMet,
			SpareCableKm: plan.Spares.TotalCableKm,
			Joints:       plan.Spares.Joints,
		}
		if busy, ok := availability[plan.VesselID]; ok {
			assignment.Contention = append(assignment.Contention, ContentionRecord{
				VesselID:    plan.VesselID,
				BusyUntil:   busy,
				FromFaultID: lastFault[plan.VesselID],
			})
		}
		if req.IncludePlans {
			trimmed := plan
			trimmed.Candidates = nil
			assignment.Plan = trimmed
		}
		result.Assignments = append(result.Assignments, assignment)

		availability[plan.VesselID] = plan.ReleaseAt
		positions[plan.VesselID] = plan.ReleaseSite
		lastFault[plan.VesselID] = fault.ID
		busyHours[plan.VesselID] += plan.TotalHours
		if cur, ok := firstStart[plan.VesselID]; !ok || plan.StartAt.Before(cur) {
			firstStart[plan.VesselID] = plan.StartAt
		}
		lastRelease[plan.VesselID] = plan.ReleaseAt
		faultsByVessel[plan.VesselID] = append(faultsByVessel[plan.VesselID], fault.ID)
		depotCable[plan.DepotID] += plan.Spares.FromDepotKm
		depotJoints[plan.DepotID] += plan.Spares.JointsFromDepot
		vesselCable[plan.VesselID] += plan.Spares.FromVesselKm
		vesselJoints[plan.VesselID] += plan.Spares.JointsFromVessel
	}

	result.Vessels = utilization(cfg, faultsByVessel, busyHours, firstStart, lastRelease, positions)
	result.DepotDraws = depotDraws(cfg, req.Assets, depotCable, depotJoints)
	costs := make([]float64, 0, len(result.Assignments))
	cable := make([]float64, 0, len(result.Assignments))
	for _, a := range result.Assignments {
		costs = append(costs, a.Cost)
		cable = append(cable, a.SpareCableKm)
		result.TotalJoints += a.Joints
		if result.FirstStart.IsZero() || a.StartAt.Before(result.FirstStart) {
			result.FirstStart = a.StartAt
		}
		if a.ReleaseAt.After(result.LastRelease) {
			result.LastRelease = a.ReleaseAt
		}
	}
	result.TotalCost = numeric.Round(numeric.SortedSum(costs), 6)
	result.TotalCableKm = cfg.Round(numeric.SortedSum(cable))
	if !result.FirstStart.IsZero() && !result.LastRelease.IsZero() {
		result.MakespanHours = cfg.Round(result.LastRelease.HoursSince(result.FirstStart))
	}
	sort.SliceStable(result.Unassigned, func(i, j int) bool {
		if result.Unassigned[i].PriorityRank != result.Unassigned[j].PriorityRank {
			return result.Unassigned[i].PriorityRank < result.Unassigned[j].PriorityRank
		}
		return result.Unassigned[i].FaultID < result.Unassigned[j].FaultID
	})
	result.Notes = campaignNotes(result)
	return result, nil
}

// scoreFaults ranks the faults. Ties fall back to the earlier report time and
// then the fault id, so the ordering is total.
func scoreFaults(cfg config.Config, req Request, faults []model.FaultRecord) []PriorityScore {
	out := make([]PriorityScore, 0, len(faults))
	for _, f := range faults {
		traffic := f.TrafficTbps
		single := f.SingleRoute
		if ix, ok := req.Systems.Get(f.SystemID); ok {
			if traffic == 0 {
				traffic = ix.System().TrafficTbps
			}
			single = single || ix.System().SingleRoute
		}
		score := cfg.Priorities.DeclaredWeight*f.DeclaredPriority + cfg.Priorities.TrafficWeightPerTbps*traffic
		if single {
			score += cfg.Priorities.SingleRouteBonus
		}
		out = append(out, PriorityScore{
			FaultID:          f.ID,
			SystemID:         f.SystemID,
			DeclaredPriority: cfg.Round(f.DeclaredPriority),
			TrafficTbps:      cfg.Round(traffic),
			SingleRoute:      single,
			ReportedAt:       f.ReportedAt,
			Score:            cfg.Round(score),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !numeric.AlmostEqual(out[i].Score, out[j].Score, 1e-9) {
			return out[i].Score > out[j].Score
		}
		if !out[i].ReportedAt.Equal(out[j].ReportedAt) {
			return out[i].ReportedAt.Before(out[j].ReportedAt)
		}
		return out[i].FaultID < out[j].FaultID
	})
	for i := range out {
		out[i].Rank = i + 1
	}
	return out
}

// applyConsumption returns a copy of the asset document with campaign stock
// consumption applied.
func applyConsumption(doc model.AssetsDocument, depotCable map[string]float64, depotJoints map[string]int,
	vesselCable map[string]float64, vesselJoints map[string]int) model.AssetsDocument {
	out := model.AssetsDocument{Version: doc.Version}
	for _, v := range doc.Vessels {
		copyVessel := v
		copyVessel.SpareCableKm = maxFloat(0, v.SpareCableKm-vesselCable[v.ID])
		copyVessel.SpareJoints = maxInt(0, v.SpareJoints-vesselJoints[v.ID])
		out.Vessels = append(out.Vessels, copyVessel)
	}
	for _, d := range doc.Depots {
		copyDepot := d
		copyDepot.SpareCableKm = maxFloat(0, d.SpareCableKm-depotCable[d.ID])
		copyDepot.SpareJoints = maxInt(0, d.SpareJoints-depotJoints[d.ID])
		out.Depots = append(out.Depots, copyDepot)
	}
	return out
}

// utilization builds the per-vessel campaign summary.
func utilization(cfg config.Config, faultsByVessel map[string][]string, busyHours map[string]float64,
	firstStart, lastRelease map[string]model.UTCTime, positions map[string]model.Site) []VesselUtilization {
	ids := make([]string, 0, len(faultsByVessel))
	for id := range faultsByVessel {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]VesselUtilization, 0, len(ids))
	for _, id := range ids {
		out = append(out, VesselUtilization{
			VesselID:    id,
			Assignments: len(faultsByVessel[id]),
			BusyHours:   cfg.Round(busyHours[id]),
			FirstStart:  firstStart[id],
			LastRelease: lastRelease[id],
			FinalSite:   positions[id],
			FaultIDs:    faultsByVessel[id],
		})
	}
	return out
}

// depotDraws reports how much stock each depot supplied.
func depotDraws(cfg config.Config, doc model.AssetsDocument, cable map[string]float64, joints map[string]int) []DepotDraw {
	out := make([]DepotDraw, 0, len(cable))
	for _, d := range doc.Depots {
		drawn := cable[d.ID]
		if drawn <= 0 && joints[d.ID] == 0 {
			continue
		}
		out = append(out, DepotDraw{
			DepotID:     d.ID,
			CableKm:     cfg.Round(drawn),
			Joints:      joints[d.ID],
			RemainingKm: cfg.Round(maxFloat(0, d.SpareCableKm-drawn)),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DepotID < out[j].DepotID })
	return out
}

// campaignNotes summarizes the outcome in plain language.
func campaignNotes(res Result) []string {
	notes := []string{
		fmt.Sprintf("%d of %d faults scheduled", len(res.Assignments), res.FaultCount),
	}
	if len(res.Unassigned) > 0 {
		notes = append(notes, fmt.Sprintf("%d faults could not be scheduled", len(res.Unassigned)))
	}
	missed := 0
	for _, a := range res.Assignments {
		if !a.DeadlineMet {
			missed++
		}
	}
	if missed > 0 {
		notes = append(notes, fmt.Sprintf("%d assignments finish after the restoration deadline", missed))
	}
	contended := 0
	for _, a := range res.Assignments {
		if len(a.Contention) > 0 {
			contended++
		}
	}
	if contended > 0 {
		notes = append(notes, fmt.Sprintf("%d assignments waited for a vessel released by an earlier repair", contended))
	}
	sort.Strings(notes)
	return notes
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
