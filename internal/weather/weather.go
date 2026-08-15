// Package weather converts hourly sea-state observations into workability
// windows. A window is a maximal run of consecutive workable hours; the run is
// broken by an unworkable hour or by a gap in the observation series. Selection
// of a working window is a pure function of the observations, the limits and the
// required duration, so two runs on the same input always pick the same window.
package weather

import (
	"fmt"
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
)

// Hour is one classified observation hour.
type Hour struct {
	Start        model.UTCTime `json:"start"`
	End          model.UTCTime `json:"end"`
	Workable     bool          `json:"workable"`
	Reason       string        `json:"reason,omitempty"`
	WaveHeightM  float64       `json:"wave_height_m"`
	WindSpeedKn  float64       `json:"wind_speed_kn"`
	VisibilityKm float64       `json:"visibility_km"`
}

// Window is a contiguous run of workable hours.
type Window struct {
	Start            model.UTCTime `json:"start"`
	End              model.UTCTime `json:"end"`
	Hours            float64       `json:"hours"`
	MaxWaveHeightM   float64       `json:"max_wave_height_m"`
	MaxWindKn        float64       `json:"max_wind_kn"`
	MinVisibilityKm  float64       `json:"min_visibility_km"`
	ObservationCount int           `json:"observation_count"`
}

// Interval renders the window as a model interval.
func (w Window) Interval() model.Interval { return model.Interval{Start: w.Start, End: w.End} }

// Gap is a missing stretch in the observation series.
type Gap struct {
	After  model.UTCTime `json:"after"`
	Before model.UTCTime `json:"before"`
	Hours  float64       `json:"hours"`
}

// ReasonCount counts how many hours were lost to one limiting factor.
type ReasonCount struct {
	Reason string  `json:"reason"`
	Hours  float64 `json:"hours"`
}

// DaySummary aggregates one UTC calendar day.
type DaySummary struct {
	Date            string        `json:"date"`
	ObservedHours   float64       `json:"observed_hours"`
	WorkableHours   float64       `json:"workable_hours"`
	LongestRunHours float64       `json:"longest_run_hours"`
	Windows         []Window      `json:"windows"`
	LimitingReasons []ReasonCount `json:"limiting_reasons"`
}

// Selection is the chosen working window.
type Selection struct {
	Found         bool          `json:"found"`
	Window        Window        `json:"window"`
	RunStart      model.UTCTime `json:"run_start"`
	RunEnd        model.UTCTime `json:"run_end"`
	RunHours      float64       `json:"run_hours"`
	RequiredHours float64       `json:"required_hours"`
	WaitHours     float64       `json:"wait_hours"`
	Reason        string        `json:"reason,omitempty"`
}

// Analysis is the full workability picture for one system.
type Analysis struct {
	SystemID         string        `json:"system_id"`
	Limits           model.Limits  `json:"limits"`
	ObservationCount int           `json:"observation_count"`
	CoverageFrom     model.UTCTime `json:"coverage_from"`
	CoverageTo       model.UTCTime `json:"coverage_to"`
	ObservationHours float64       `json:"observation_hours"`
	WorkableHours    float64       `json:"workable_hours"`
	Days             []DaySummary  `json:"days"`
	Windows          []Window      `json:"windows"`
	Gaps             []Gap         `json:"gaps"`
	Hours            []Hour        `json:"hours,omitempty"`
	Selection        Selection     `json:"selection"`
}

// run is an internal contiguous workable stretch.
type run struct {
	hours []Hour
}

// Analyze classifies the observations of one system and selects the earliest
// window of at least requiredHours starting no earlier than notBefore.
func Analyze(cfg config.Config, systemID string, obs []model.Observation, limits model.Limits, requiredHours float64, notBefore model.UTCTime, includeHours bool) (Analysis, error) {
	filtered := model.FilterObservations(obs, systemID)
	if len(filtered) == 0 {
		return Analysis{}, fmt.Errorf("no sea-state observations for system %q", systemID)
	}
	span := cfg.Weather.ObservationHours
	analysis := Analysis{
		SystemID:         systemID,
		Limits:           limits,
		ObservationCount: len(filtered),
	}

	hours := make([]Hour, 0, len(filtered))
	for i, o := range filtered {
		if i > 0 && filtered[i-1].HourStart.Equal(o.HourStart) {
			return Analysis{}, fmt.Errorf("system %s: duplicate observation for hour %s", systemID, o.HourStart)
		}
		workable, reason := limits.Workable(o)
		hours = append(hours, Hour{
			Start:        o.HourStart,
			End:          o.HourStart.AddHours(span),
			Workable:     workable,
			Reason:       reason,
			WaveHeightM:  cfg.Round(o.WaveHeightM),
			WindSpeedKn:  cfg.Round(o.WindSpeedKn),
			VisibilityKm: cfg.Round(o.VisibilityKm),
		})
	}
	analysis.CoverageFrom = hours[0].Start
	analysis.CoverageTo = hours[len(hours)-1].End
	analysis.ObservationHours = cfg.Round(float64(len(hours)) * span)

	runs := splitRuns(hours)
	for _, r := range runs {
		analysis.Windows = append(analysis.Windows, windowFromHours(cfg, r.hours))
	}
	analysis.Gaps = findGaps(cfg, hours)
	workable := 0.0
	for _, h := range hours {
		if h.Workable {
			workable += span
		}
	}
	analysis.WorkableHours = cfg.Round(workable)
	analysis.Days = summarizeDays(cfg, hours, runs, span)
	analysis.Selection = selectRun(cfg, runs, requiredHours, notBefore)
	if includeHours {
		analysis.Hours = hours
	}
	return analysis, nil
}

// splitRuns groups consecutive workable hours, breaking on unworkable hours and
// on discontinuities in the series.
func splitRuns(hours []Hour) []run {
	var runs []run
	var current []Hour
	flush := func() {
		if len(current) > 0 {
			runs = append(runs, run{hours: current})
			current = nil
		}
	}
	for i, h := range hours {
		if !h.Workable {
			flush()
			continue
		}
		if i > 0 && len(current) > 0 {
			prev := hours[i-1]
			if !prev.End.Equal(h.Start) {
				flush()
			}
		}
		current = append(current, h)
	}
	flush()
	return runs
}

// findGaps reports discontinuities in the observation series.
func findGaps(cfg config.Config, hours []Hour) []Gap {
	var gaps []Gap
	for i := 1; i < len(hours); i++ {
		prev := hours[i-1]
		cur := hours[i]
		if prev.End.Equal(cur.Start) {
			continue
		}
		missing := cur.Start.HoursSince(prev.End)
		if missing <= 0 {
			continue
		}
		gaps = append(gaps, Gap{After: prev.End, Before: cur.Start, Hours: cfg.Round(missing)})
	}
	return gaps
}

// windowFromHours summarizes a contiguous run.
func windowFromHours(cfg config.Config, hours []Hour) Window {
	w := Window{
		Start:            hours[0].Start,
		End:              hours[len(hours)-1].End,
		ObservationCount: len(hours),
		MinVisibilityKm:  hours[0].VisibilityKm,
	}
	for _, h := range hours {
		if h.WaveHeightM > w.MaxWaveHeightM {
			w.MaxWaveHeightM = h.WaveHeightM
		}
		if h.WindSpeedKn > w.MaxWindKn {
			w.MaxWindKn = h.WindSpeedKn
		}
		if h.VisibilityKm < w.MinVisibilityKm {
			w.MinVisibilityKm = h.VisibilityKm
		}
	}
	w.Hours = cfg.Round(w.End.HoursSince(w.Start))
	w.MaxWaveHeightM = cfg.Round(w.MaxWaveHeightM)
	w.MaxWindKn = cfg.Round(w.MaxWindKn)
	w.MinVisibilityKm = cfg.Round(w.MinVisibilityKm)
	return w
}

// summarizeDays produces the per-day workability view.
func summarizeDays(cfg config.Config, hours []Hour, runs []run, span float64) []DaySummary {
	type acc struct {
		observed float64
		workable float64
		reasons  map[string]float64
	}
	byDay := map[string]*acc{}
	var order []string
	for _, h := range hours {
		key := h.Start.DateKey()
		a, ok := byDay[key]
		if !ok {
			a = &acc{reasons: map[string]float64{}}
			byDay[key] = a
			order = append(order, key)
		}
		a.observed += span
		if h.Workable {
			a.workable += span
			continue
		}
		a.reasons[h.Reason] += span
	}
	sort.Strings(order)
	windowsByDay := map[string][]Window{}
	longestByDay := map[string]float64{}
	for _, r := range runs {
		for _, part := range splitRunByDay(r.hours) {
			key := part[0].Start.DateKey()
			w := windowFromHours(cfg, part)
			windowsByDay[key] = append(windowsByDay[key], w)
			if w.Hours > longestByDay[key] {
				longestByDay[key] = w.Hours
			}
		}
	}
	out := make([]DaySummary, 0, len(order))
	for _, key := range order {
		a := byDay[key]
		day := DaySummary{
			Date:            key,
			ObservedHours:   cfg.Round(a.observed),
			WorkableHours:   cfg.Round(a.workable),
			LongestRunHours: cfg.Round(longestByDay[key]),
			Windows:         windowsByDay[key],
		}
		reasons := make([]string, 0, len(a.reasons))
		for r := range a.reasons {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		for _, r := range reasons {
			day.LimitingReasons = append(day.LimitingReasons, ReasonCount{Reason: r, Hours: cfg.Round(a.reasons[r])})
		}
		out = append(out, day)
	}
	return out
}

// splitRunByDay divides a run at UTC midnight so the per-day view never reports
// a window belonging to two days.
func splitRunByDay(hours []Hour) [][]Hour {
	var out [][]Hour
	var current []Hour
	for _, h := range hours {
		if len(current) > 0 && current[len(current)-1].Start.DateKey() != h.Start.DateKey() {
			out = append(out, current)
			current = nil
		}
		current = append(current, h)
	}
	if len(current) > 0 {
		out = append(out, current)
	}
	return out
}

// selectRun picks the earliest run that can host requiredHours of continuous
// work at or after notBefore.
func selectRun(cfg config.Config, runs []run, requiredHours float64, notBefore model.UTCTime) Selection {
	sel := Selection{RequiredHours: cfg.Round(requiredHours)}
	if requiredHours <= 0 {
		sel.Reason = "required duration must be positive"
		return sel
	}
	if len(runs) == 0 {
		sel.Reason = "no workable hours in the observation set"
		return sel
	}
	for _, r := range runs {
		if len(r.hours) == 0 {
			continue
		}
		runStart := r.hours[0].Start
		runEnd := r.hours[len(r.hours)-1].End
		start := runStart
		if !notBefore.IsZero() && notBefore.After(start) {
			start = notBefore
		}
		available := runEnd.HoursSince(start)
		if available < requiredHours-1e-9 {
			continue
		}
		end := start.AddHours(requiredHours)
		sel.Found = true
		sel.Window = windowFromHours(cfg, hoursInSpan(r.hours, start, end))
		sel.Window.Start = start
		sel.Window.End = end
		sel.Window.Hours = cfg.Round(requiredHours)
		sel.RunStart = runStart
		sel.RunEnd = runEnd
		sel.RunHours = cfg.Round(runEnd.HoursSince(runStart))
		if !notBefore.IsZero() {
			sel.WaitHours = cfg.Round(start.HoursSince(notBefore))
		}
		return sel
	}
	sel.Reason = fmt.Sprintf("no workable run of %s hours at or after %s",
		numeric.Format(requiredHours, cfg.Decimals()), notBeforeLabel(notBefore))
	return sel
}

// hoursInSpan returns the observation hours overlapping [start, end).
func hoursInSpan(hours []Hour, start, end model.UTCTime) []Hour {
	out := make([]Hour, 0, len(hours))
	for _, h := range hours {
		if h.End.Before(start) || h.End.Equal(start) {
			continue
		}
		if !h.Start.Before(end) {
			continue
		}
		out = append(out, h)
	}
	if len(out) == 0 && len(hours) > 0 {
		out = append(out, hours[0])
	}
	return out
}

func notBeforeLabel(t model.UTCTime) string {
	if t.IsZero() {
		return "the start of the series"
	}
	return t.String()
}

// LimitsFor merges the configured limits with an optional vessel sea-state cap.
func LimitsFor(cfg config.Config, vesselSeaStateM float64) model.Limits {
	limits := model.Limits{
		MaxWaveHeightM:  cfg.Weather.MaxWaveHeightM,
		MaxWindKn:       cfg.Weather.MaxWindKn,
		MinVisibilityKm: cfg.Weather.MinVisibilityKm,
	}
	if vesselSeaStateM > 0 {
		limits = limits.Tighten(model.Limits{MaxWaveHeightM: vesselSeaStateM})
	}
	return limits
}
