// Package planner turns a localization into an executable repair plan: which
// vessel sails from which depot, when each stage happens, how much cable and how
// many joints are consumed, and which permits authorize the work. Every choice
// is a deterministic function of the inputs.
package planner

import (
	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/permits"
	"CableMend/internal/weather"
)

// Stage names in execution order.
const (
	StageMobilize    = "mobilize"
	StageTransit     = "transit"
	StageStandby     = "standby"
	StageSurvey      = "survey"
	StageCutAndHold  = "cut_and_hold"
	StageSplice      = "splice"
	StageTest        = "test"
	StageFinalBurial = "final_burial"
	StageDemobilize  = "demobilize"
)

// Infeasibility reasons.
const (
	ReasonNoDepotAccess     = "depot_has_no_access_to_system"
	ReasonDepthCapability   = "water_depth_exceeds_vessel_capability"
	ReasonNoSubseaVehicle   = "vessel_has_no_rov_or_auv"
	ReasonROVRequired       = "buried_cable_requires_rov"
	ReasonSpareCable        = "insufficient_spare_cable"
	ReasonSpareJoints       = "insufficient_spare_joints"
	ReasonNoWeatherWindow   = "no_weather_window_of_required_length"
	ReasonNoPermitWindow    = "no_permit_covers_a_workable_window"
	ReasonUnknownDepth      = "no_depth_profile_for_window"
	ReasonRepositionUnknown = "reposition_distance_unknown"
)

// Risk flag names.
const (
	RiskSpareShortfall    = "spare_shortfall"
	RiskJointLimit        = "segment_joint_limit_reached"
	RiskDeadlineMissed    = "restoration_deadline_missed"
	RiskAnchoringZone     = "anchoring_restricted_zone"
	RiskInconsistent      = "localization_inconsistent"
	RiskNoDepth           = "no_depth_profile"
	RiskPermitTailNarrow  = "permit_expires_within_work_margin"
	RiskDeepWater         = "deep_water_operation"
	RiskWideWindow        = "wide_localization_window"
	RiskNoSpareAtDepot    = "depot_stock_depleted_by_plan"
	RiskSingleRouteSystem = "system_has_no_diverse_route"
)

// Stage is one scheduled activity.
type Stage struct {
	Order           int           `json:"order"`
	Name            string        `json:"name"`
	Operation       string        `json:"operation,omitempty"`
	StartAt         model.UTCTime `json:"start_at"`
	EndAt           model.UTCTime `json:"end_at"`
	Hours           float64       `json:"hours"`
	CumulativeHours float64       `json:"cumulative_hours"`
	OnSite          bool          `json:"on_site"`
	Note            string        `json:"note,omitempty"`
}

// SpareUsage is the cable and joint consumption of one repair.
type SpareUsage struct {
	ReplacedRouteKm  float64 `json:"replaced_route_km"`
	SegmentSlack     float64 `json:"segment_slack"`
	BaseCableKm      float64 `json:"base_cable_km"`
	BightAllowanceKm float64 `json:"bight_allowance_km"`
	SlackAllowanceKm float64 `json:"slack_allowance_km"`
	TotalCableKm     float64 `json:"total_cable_km"`
	Joints           int     `json:"joints"`
	FromVesselKm     float64 `json:"from_vessel_km"`
	FromDepotKm      float64 `json:"from_depot_km"`
	ShortfallKm      float64 `json:"shortfall_km"`
	JointsFromVessel int     `json:"joints_from_vessel"`
	JointsFromDepot  int     `json:"joints_from_depot"`
	JointShortfall   int     `json:"joint_shortfall"`
	Sufficient       bool    `json:"sufficient"`
}

// CostItem is one addend of the selection cost.
type CostItem struct {
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
	Detail string  `json:"detail,omitempty"`
}

// Candidate is one evaluated vessel and depot pairing.
type Candidate struct {
	VesselID          string             `json:"vessel_id"`
	VesselName        string             `json:"vessel_name"`
	DepotID           string             `json:"depot_id"`
	DepotName         string             `json:"depot_name"`
	Feasible          bool               `json:"feasible"`
	Reasons           []string           `json:"reasons,omitempty"`
	OriginKind        string             `json:"origin_kind"`
	OriginID          string             `json:"origin_id"`
	ToDepotNm         float64            `json:"to_depot_nm"`
	DepotToSiteNm     float64            `json:"depot_to_site_nm"`
	TransitNm         float64            `json:"transit_nm"`
	TransitHours      float64            `json:"transit_hours"`
	MobilizationHours float64            `json:"mobilization_hours"`
	StandbyHours      float64            `json:"standby_hours"`
	OnSiteHours       float64            `json:"on_site_hours"`
	DemobHours        float64            `json:"demobilization_hours"`
	TotalHours        float64            `json:"total_hours"`
	ReadyAt           model.UTCTime      `json:"ready_at"`
	OnSiteFrom        model.UTCTime      `json:"on_site_from"`
	CompletesAt       model.UTCTime      `json:"completes_at"`
	Cost              float64            `json:"cost"`
	CostItems         []CostItem         `json:"cost_items"`
	Spares            SpareUsage         `json:"spares"`
	Window            weather.Window     `json:"window"`
	WindowFound       bool               `json:"window_found"`
	Permits           permits.Assessment `json:"permits"`
	Attempts          int                `json:"scheduling_attempts"`
}

// Plan is the selected repair plan for one fault.
type Plan struct {
	FaultID        string             `json:"fault_id"`
	SystemID       string             `json:"system_id"`
	SystemName     string             `json:"system_name"`
	AsOf           model.UTCTime      `json:"as_of"`
	Feasible       bool               `json:"feasible"`
	Reasons        []string           `json:"reasons,omitempty"`
	Site           model.Site         `json:"site"`
	Localization   LocalizationBrief  `json:"localization"`
	VesselID       string             `json:"vessel_id,omitempty"`
	VesselName     string             `json:"vessel_name,omitempty"`
	DepotID        string             `json:"depot_id,omitempty"`
	DepotName      string             `json:"depot_name,omitempty"`
	Cost           float64            `json:"cost"`
	CostItems      []CostItem         `json:"cost_items,omitempty"`
	Window         weather.Window     `json:"window"`
	Limits         model.Limits       `json:"weather_limits"`
	Permits        permits.Assessment `json:"permits"`
	Spares         SpareUsage         `json:"spares"`
	Stages         []Stage            `json:"stages"`
	StartAt        model.UTCTime      `json:"start_at"`
	EndAt          model.UTCTime      `json:"end_at"`
	TotalHours     float64            `json:"total_hours"`
	OnSiteHours    float64            `json:"on_site_hours"`
	RestoreBy      model.UTCTime      `json:"restore_by"`
	DeadlineMet    bool               `json:"deadline_met"`
	RiskFlags      []string           `json:"risk_flags"`
	RiskScore      float64            `json:"risk_score"`
	Notes          []string           `json:"notes,omitempty"`
	Candidates     []Candidate        `json:"candidates"`
	ReleaseSite    model.Site         `json:"release_site"`
	ReleaseAt      model.UTCTime      `json:"release_at"`
	Operations     []string           `json:"operations"`
	SegmentID      string             `json:"segment_id"`
	BurialLengthKm float64            `json:"burial_length_km"`
}

// LocalizationBrief is the summary of a localization carried inside a plan.
type LocalizationBrief struct {
	BestKP        float64          `json:"best_kp"`
	WindowLowKP   float64          `json:"window_low_kp"`
	WindowHighKP  float64          `json:"window_high_kp"`
	WindowWidthKm float64          `json:"window_width_km"`
	UncertaintyKm float64          `json:"uncertainty_km"`
	FaultClass    model.FaultClass `json:"fault_class"`
	Consistent    bool             `json:"consistent"`
	Flags         []string         `json:"flags"`
	SegmentID     string           `json:"segment_id"`
	DepthM        float64          `json:"depth_m"`
	BurialDepthM  float64          `json:"burial_depth_m"`
	EvidenceCount int              `json:"evidence_count"`
	Zones         []string         `json:"zones"`
}

// briefOf condenses a localization result for embedding in a plan.
func briefOf(res locate.Result) LocalizationBrief {
	return LocalizationBrief{
		BestKP:        res.BestKP,
		WindowLowKP:   res.WindowLowKP,
		WindowHighKP:  res.WindowHighKP,
		WindowWidthKm: res.WindowWidthKm,
		UncertaintyKm: res.UncertaintyKm,
		FaultClass:    res.FaultClass,
		Consistent:    res.Consistent,
		Flags:         res.Flags,
		SegmentID:     res.SegmentID,
		DepthM:        res.Depth.DepthM,
		BurialDepthM:  res.Depth.BurialDepthM,
		EvidenceCount: res.EvidenceCount,
		Zones:         zoneIDs(res.Zones),
	}
}

func zoneIDs(zones []locate.ZoneRef) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		out = append(out, z.ID)
	}
	return out
}
