// Package config defines the CableMend runtime configuration: the tunable
// engineering constants behind fault localization, window search, repair
// scheduling and verification. Configuration is loaded strictly; unknown fields
// are an error, and omitted fields keep their documented default.
package config

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/jsonio"
	"CableMend/internal/numeric"
)

// SchemaVersion is the only accepted config document version.
const SchemaVersion = 1

// Config is the full configuration document.
type Config struct {
	Version      int             `json:"version"`
	StoreDir     string          `json:"store_dir"`
	Localization Localization    `json:"localization"`
	Repair       Repair          `json:"repair"`
	Weather      Weather         `json:"weather"`
	Costs        Costs           `json:"costs"`
	Verification Verification    `json:"verification"`
	Output       OutputSettings  `json:"output"`
	Priorities   PriorityWeights `json:"priorities"`
}

// Localization controls how measured cable distances become route positions.
type Localization struct {
	DefaultSlackFactor      float64 `json:"default_slack_factor"`
	DisagreementToleranceKm float64 `json:"disagreement_tolerance_km"`
	OTDRUncertaintyKm       float64 `json:"otdr_uncertainty_km"`
	ResistanceUncertaintyKm float64 `json:"resistance_uncertainty_km"`
	VoltageUncertaintyKm    float64 `json:"voltage_uncertainty_km"`
	RelativeUncertainty     float64 `json:"relative_uncertainty"`
	CoverageFactor          float64 `json:"coverage_factor"`
	MinWindowKm             float64 `json:"min_window_km"`
	MaxWindowKm             float64 `json:"max_window_km"`
	MinEvidenceRecords      int     `json:"min_evidence_records"`
	ShuntResistanceMohm     float64 `json:"shunt_resistance_mohm"`
}

// Repair holds the stage durations and cable consumption rules.
type Repair struct {
	SurveyHours         float64 `json:"survey_hours"`
	CutAndHoldHours     float64 `json:"cut_and_hold_hours"`
	SpliceHoursPerJoint float64 `json:"splice_hours_per_joint"`
	TestHours           float64 `json:"test_hours"`
	BurialHoursPerKm    float64 `json:"burial_hours_per_km"`
	MinWorkHours        float64 `json:"min_work_hours"`
	JointsPerRepair     int     `json:"joints_per_repair"`
	SpareSlackRatio     float64 `json:"spare_slack_ratio"`
	ExcessCableKm       float64 `json:"excess_cable_km"`
	BightFactor         float64 `json:"bight_factor"`
	DepthMarginM        float64 `json:"depth_margin_m"`
	DemobilizationHours float64 `json:"demobilization_hours"`
}

// Weather holds the default workability thresholds. Vessel limits, when
// stricter, override the wave threshold.
type Weather struct {
	MaxWaveHeightM   float64 `json:"max_wave_height_m"`
	MaxWindKn        float64 `json:"max_wind_kn"`
	MinVisibilityKm  float64 `json:"min_visibility_km"`
	ObservationHours float64 `json:"observation_hours"`
}

// Costs holds the deterministic vessel/depot selection weights.
type Costs struct {
	DayRateWeight            float64 `json:"day_rate_weight"`
	TransitHourWeight        float64 `json:"transit_hour_weight"`
	MobilizationHourWeight   float64 `json:"mobilization_hour_weight"`
	StandbyHourWeight        float64 `json:"standby_hour_weight"`
	SpareShortfallPerKm      float64 `json:"spare_shortfall_per_km"`
	MissingCapabilityPenalty float64 `json:"missing_capability_penalty"`
	DepotTransferPerKm       float64 `json:"depot_transfer_per_km"`
}

// Verification holds the post-repair acceptance thresholds.
type Verification struct {
	LossToleranceDb         float64 `json:"loss_tolerance_db"`
	JointLossDb             float64 `json:"joint_loss_db"`
	MaxJointsPerSegment     int     `json:"max_joints_per_segment"`
	RiskDepthThresholdM     float64 `json:"risk_depth_threshold_m"`
	BurialDeficitToleranceM float64 `json:"burial_deficit_tolerance_m"`
}

// OutputSettings controls rendering precision.
type OutputSettings struct {
	FloatDecimals int `json:"float_decimals"`
}

// PriorityWeights turns traffic impact into a campaign priority score.
type PriorityWeights struct {
	TrafficWeightPerTbps float64 `json:"traffic_weight_per_tbps"`
	DeclaredWeight       float64 `json:"declared_weight"`
	SingleRouteBonus     float64 `json:"single_route_bonus"`
}

// Default returns the built-in configuration. The values are plausible
// engineering defaults for a fictional deep-water repair programme.
func Default() Config {
	return Config{
		Version:  SchemaVersion,
		StoreDir: ".cablemend",
		Localization: Localization{
			DefaultSlackFactor:      1.03,
			DisagreementToleranceKm: 2.5,
			OTDRUncertaintyKm:       0.3,
			ResistanceUncertaintyKm: 3.0,
			VoltageUncertaintyKm:    5.0,
			RelativeUncertainty:     0.002,
			CoverageFactor:          2.0,
			MinWindowKm:             0.5,
			MaxWindowKm:             40.0,
			MinEvidenceRecords:      1,
			ShuntResistanceMohm:     1.0,
		},
		Repair: Repair{
			SurveyHours:         8.0,
			CutAndHoldHours:     12.0,
			SpliceHoursPerJoint: 6.0,
			TestHours:           4.0,
			BurialHoursPerKm:    1.5,
			MinWorkHours:        12.0,
			JointsPerRepair:     2,
			SpareSlackRatio:     0.12,
			ExcessCableKm:       2.0,
			BightFactor:         2.2,
			DepthMarginM:        150.0,
			DemobilizationHours: 12.0,
		},
		Weather: Weather{
			MaxWaveHeightM:   2.5,
			MaxWindKn:        30.0,
			MinVisibilityKm:  1.0,
			ObservationHours: 1.0,
		},
		Costs: Costs{
			DayRateWeight:            1.0,
			TransitHourWeight:        1.0,
			MobilizationHourWeight:   1.25,
			StandbyHourWeight:        0.6,
			SpareShortfallPerKm:      400.0,
			MissingCapabilityPenalty: 5000.0,
			DepotTransferPerKm:       12.0,
		},
		Verification: Verification{
			LossToleranceDb:         0.5,
			JointLossDb:             0.15,
			MaxJointsPerSegment:     6,
			RiskDepthThresholdM:     1500.0,
			BurialDeficitToleranceM: 0.2,
		},
		Output: OutputSettings{FloatDecimals: 3},
		Priorities: PriorityWeights{
			TrafficWeightPerTbps: 4.0,
			DeclaredWeight:       1.0,
			SingleRouteBonus:     25.0,
		},
	}
}

// Load reads a configuration document, applying it on top of the defaults so
// that partial documents remain valid.
func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	if err := jsonio.DecodeFile(path, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks every field for a usable value and returns all problems at
// once, sorted for deterministic reporting.
func (c Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}
	if c.Version != SchemaVersion {
		add("version must be %d, got %d", SchemaVersion, c.Version)
	}
	if strings.TrimSpace(c.StoreDir) == "" {
		add("store_dir must not be empty")
	}
	l := c.Localization
	if l.DefaultSlackFactor < 1.0 || l.DefaultSlackFactor > 1.5 {
		add("localization.default_slack_factor must be within [1.0, 1.5], got %g", l.DefaultSlackFactor)
	}
	if !numeric.Positive(l.DisagreementToleranceKm) {
		add("localization.disagreement_tolerance_km must be positive")
	}
	for name, v := range map[string]float64{
		"localization.otdr_uncertainty_km":       l.OTDRUncertaintyKm,
		"localization.resistance_uncertainty_km": l.ResistanceUncertaintyKm,
		"localization.voltage_uncertainty_km":    l.VoltageUncertaintyKm,
	} {
		if !numeric.Positive(v) {
			add("%s must be positive", name)
		}
	}
	if !numeric.NonNegative(l.RelativeUncertainty) || l.RelativeUncertainty > 0.5 {
		add("localization.relative_uncertainty must be within [0, 0.5]")
	}
	if !numeric.Positive(l.CoverageFactor) {
		add("localization.coverage_factor must be positive")
	}
	if !numeric.Positive(l.MinWindowKm) {
		add("localization.min_window_km must be positive")
	}
	if l.MaxWindowKm <= l.MinWindowKm {
		add("localization.max_window_km must exceed min_window_km")
	}
	if l.MinEvidenceRecords < 1 {
		add("localization.min_evidence_records must be at least 1")
	}
	if !numeric.Positive(l.ShuntResistanceMohm) {
		add("localization.shunt_resistance_mohm must be positive")
	}
	r := c.Repair
	for name, v := range map[string]float64{
		"repair.survey_hours":           r.SurveyHours,
		"repair.cut_and_hold_hours":     r.CutAndHoldHours,
		"repair.splice_hours_per_joint": r.SpliceHoursPerJoint,
		"repair.test_hours":             r.TestHours,
		"repair.burial_hours_per_km":    r.BurialHoursPerKm,
		"repair.min_work_hours":         r.MinWorkHours,
		"repair.bight_factor":           r.BightFactor,
	} {
		if !numeric.Positive(v) {
			add("%s must be positive", name)
		}
	}
	if r.JointsPerRepair < 1 || r.JointsPerRepair > 8 {
		add("repair.joints_per_repair must be within [1, 8], got %d", r.JointsPerRepair)
	}
	if !numeric.NonNegative(r.SpareSlackRatio) || r.SpareSlackRatio > 1 {
		add("repair.spare_slack_ratio must be within [0, 1]")
	}
	if !numeric.NonNegative(r.ExcessCableKm) {
		add("repair.excess_cable_km must not be negative")
	}
	if !numeric.NonNegative(r.DepthMarginM) {
		add("repair.depth_margin_m must not be negative")
	}
	if !numeric.NonNegative(r.DemobilizationHours) {
		add("repair.demobilization_hours must not be negative")
	}
	w := c.Weather
	if !numeric.Positive(w.MaxWaveHeightM) {
		add("weather.max_wave_height_m must be positive")
	}
	if !numeric.Positive(w.MaxWindKn) {
		add("weather.max_wind_kn must be positive")
	}
	if !numeric.NonNegative(w.MinVisibilityKm) {
		add("weather.min_visibility_km must not be negative")
	}
	if !numeric.Positive(w.ObservationHours) || w.ObservationHours > 24 {
		add("weather.observation_hours must be within (0, 24]")
	}
	cost := c.Costs
	for name, v := range map[string]float64{
		"costs.day_rate_weight":            cost.DayRateWeight,
		"costs.transit_hour_weight":        cost.TransitHourWeight,
		"costs.mobilization_hour_weight":   cost.MobilizationHourWeight,
		"costs.standby_hour_weight":        cost.StandbyHourWeight,
		"costs.spare_shortfall_per_km":     cost.SpareShortfallPerKm,
		"costs.missing_capability_penalty": cost.MissingCapabilityPenalty,
		"costs.depot_transfer_per_km":      cost.DepotTransferPerKm,
	} {
		if !numeric.NonNegative(v) {
			add("%s must not be negative", name)
		}
	}
	v := c.Verification
	if !numeric.Positive(v.LossToleranceDb) {
		add("verification.loss_tolerance_db must be positive")
	}
	if !numeric.NonNegative(v.JointLossDb) {
		add("verification.joint_loss_db must not be negative")
	}
	if v.MaxJointsPerSegment < 1 {
		add("verification.max_joints_per_segment must be at least 1")
	}
	if !numeric.Positive(v.RiskDepthThresholdM) {
		add("verification.risk_depth_threshold_m must be positive")
	}
	if !numeric.NonNegative(v.BurialDeficitToleranceM) {
		add("verification.burial_deficit_tolerance_m must not be negative")
	}
	if c.Output.FloatDecimals < 0 || c.Output.FloatDecimals > numeric.MaxDecimals {
		add("output.float_decimals must be within [0, %d]", numeric.MaxDecimals)
	}
	p := c.Priorities
	if !numeric.NonNegative(p.TrafficWeightPerTbps) {
		add("priorities.traffic_weight_per_tbps must not be negative")
	}
	if !numeric.NonNegative(p.DeclaredWeight) {
		add("priorities.declared_weight must not be negative")
	}
	if !numeric.NonNegative(p.SingleRouteBonus) {
		add("priorities.single_route_bonus must not be negative")
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
}

// OnSiteHours returns the total on-site working hours for one repair given the
// burial length in kilometres and the number of joints.
func (c Config) OnSiteHours(burialKm float64, joints int) float64 {
	hours := c.Repair.SurveyHours + c.Repair.CutAndHoldHours + c.Repair.TestHours
	hours += c.Repair.SpliceHoursPerJoint * float64(joints)
	if burialKm > 0 {
		hours += c.Repair.BurialHoursPerKm * burialKm
	}
	if hours < c.Repair.MinWorkHours {
		hours = c.Repair.MinWorkHours
	}
	return hours
}

// Decimals is the configured output precision.
func (c Config) Decimals() int {
	return numeric.ClampInt(c.Output.FloatDecimals, 0, numeric.MaxDecimals)
}

// Round applies the configured precision.
func (c Config) Round(v float64) float64 { return numeric.Round(v, c.Decimals()) }
