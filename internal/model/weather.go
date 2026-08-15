package model

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/numeric"
)

// Observation is one hourly sea-state observation for a system.
type Observation struct {
	SystemID     string  `json:"system_id"`
	HourStart    UTCTime `json:"hour_start"`
	WaveHeightM  float64 `json:"wave_height_m"`
	WindSpeedKn  float64 `json:"wind_speed_kn"`
	VisibilityKm float64 `json:"visibility_km"`
	CurrentKn    float64 `json:"current_kn"`
	Source       string  `json:"source"`
}

// Validate checks one observation.
func (o Observation) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("observation %s %s: %s", o.SystemID, o.HourStart, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(o.SystemID) == "" {
		problems = append(problems, "observation: system_id must not be empty")
	}
	if o.HourStart.IsZero() {
		add("hour_start must be set")
	}
	if !numeric.NonNegative(o.WaveHeightM) {
		add("wave_height_m must not be negative")
	}
	if !numeric.NonNegative(o.WindSpeedKn) {
		add("wind_speed_kn must not be negative")
	}
	if !numeric.NonNegative(o.VisibilityKm) {
		add("visibility_km must not be negative")
	}
	if !numeric.NonNegative(o.CurrentKn) {
		add("current_kn must not be negative")
	}
	return problems
}

// SortObservations orders observations by system then hour.
func SortObservations(obs []Observation) []Observation {
	out := make([]Observation, len(obs))
	copy(out, obs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SystemID != out[j].SystemID {
			return out[i].SystemID < out[j].SystemID
		}
		return out[i].HourStart.Before(out[j].HourStart)
	})
	return out
}

// FilterObservations keeps observations for one system.
func FilterObservations(obs []Observation, systemID string) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if systemID == "" || o.SystemID == systemID {
			out = append(out, o)
		}
	}
	return SortObservations(out)
}

// Limits is the workability threshold set applied to observations.
type Limits struct {
	MaxWaveHeightM  float64 `json:"max_wave_height_m"`
	MaxWindKn       float64 `json:"max_wind_kn"`
	MinVisibilityKm float64 `json:"min_visibility_km"`
}

// Workable reports whether an observation satisfies the limits and, when it does
// not, the reason with the lowest alphabetical name for stable reporting.
func (l Limits) Workable(o Observation) (bool, string) {
	var reasons []string
	if o.WaveHeightM > l.MaxWaveHeightM+1e-9 {
		reasons = append(reasons, "sea_state")
	}
	if o.WindSpeedKn > l.MaxWindKn+1e-9 {
		reasons = append(reasons, "wind")
	}
	if o.VisibilityKm < l.MinVisibilityKm-1e-9 {
		reasons = append(reasons, "visibility")
	}
	if len(reasons) == 0 {
		return true, ""
	}
	sort.Strings(reasons)
	return false, strings.Join(reasons, "+")
}

// Tighten returns the stricter of two limit sets, field by field.
func (l Limits) Tighten(other Limits) Limits {
	out := l
	if other.MaxWaveHeightM > 0 && other.MaxWaveHeightM < out.MaxWaveHeightM {
		out.MaxWaveHeightM = other.MaxWaveHeightM
	}
	if other.MaxWindKn > 0 && other.MaxWindKn < out.MaxWindKn {
		out.MaxWindKn = other.MaxWindKn
	}
	if other.MinVisibilityKm > out.MinVisibilityKm {
		out.MinVisibilityKm = other.MinVisibilityKm
	}
	return out
}
