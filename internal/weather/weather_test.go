package weather

import (
	"testing"

	"CableMend/internal/config"
	"CableMend/internal/model"
)

// series builds hourly observations from a wave-height pattern starting at the
// given instant.
func series(systemID string, start model.UTCTime, waves []float64, skip map[int]bool) []model.Observation {
	out := make([]model.Observation, 0, len(waves))
	for i, wave := range waves {
		if skip[i] {
			continue
		}
		out = append(out, model.Observation{
			SystemID:     systemID,
			HourStart:    start.AddHours(float64(i)),
			WaveHeightM:  wave,
			WindSpeedKn:  12,
			VisibilityKm: 10,
			Source:       "TEST",
		})
	}
	return out
}

func TestAnalyzeSplitsRunsOnUnworkableHours(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1, 1, 1, 4, 4, 1, 1, 1, 1}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), LimitsFor(cfg, 0), 3, model.UTCTime{}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(analysis.Windows) != 2 {
		t.Fatalf("windows = %d, want 2", len(analysis.Windows))
	}
	if analysis.Windows[0].Hours != 3 || analysis.Windows[1].Hours != 4 {
		t.Fatalf("window hours = %v, %v", analysis.Windows[0].Hours, analysis.Windows[1].Hours)
	}
	if analysis.WorkableHours != 7 {
		t.Fatalf("workable hours = %v, want 7", analysis.WorkableHours)
	}
	if len(analysis.Hours) != len(waves) {
		t.Fatalf("hourly series = %d rows, want %d", len(analysis.Hours), len(waves))
	}
}

func TestAnalyzeBreaksRunsOnObservationGaps(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1, 1, 1, 1, 1, 1}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, map[int]bool{3: true}), LimitsFor(cfg, 0), 2, model.UTCTime{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(analysis.Windows) != 2 {
		t.Fatalf("windows = %d, want the gap to split the run", len(analysis.Windows))
	}
	if len(analysis.Gaps) != 1 || analysis.Gaps[0].Hours != 1 {
		t.Fatalf("gaps = %+v", analysis.Gaps)
	}
}

func TestSelectionPicksEarliestFeasibleWindow(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1, 1, 4, 1, 1, 1, 1, 1}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), LimitsFor(cfg, 0), 4, model.UTCTime{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sel := analysis.Selection
	if !sel.Found {
		t.Fatalf("expected a window, reason %q", sel.Reason)
	}
	if sel.Window.Start.String() != "2026-03-10T03:00:00Z" {
		t.Fatalf("window start = %s, want the second run", sel.Window.Start)
	}
	if sel.Window.Hours != 4 {
		t.Fatalf("window hours = %v, want exactly the requested duration", sel.Window.Hours)
	}
	if sel.RunHours != 5 {
		t.Fatalf("host run hours = %v, want 5", sel.RunHours)
	}
}

func TestSelectionHonoursNotBefore(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1, 1, 1, 1, 1, 1, 1, 1}
	notBefore := model.MustParseUTC("2026-03-10T03:00:00Z")
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), LimitsFor(cfg, 0), 4, notBefore, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis.Selection.Window.Start.String() != "2026-03-10T03:00:00Z" {
		t.Fatalf("window start = %s, want the not-before instant", analysis.Selection.Window.Start)
	}
	if analysis.Selection.WaitHours != 0 {
		t.Fatalf("wait hours = %v, want 0", analysis.Selection.WaitHours)
	}
}

func TestSelectionFailsWhenNoRunIsLongEnough(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1, 1, 4, 1, 1}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), LimitsFor(cfg, 0), 6, model.UTCTime{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis.Selection.Found {
		t.Fatal("expected no window")
	}
	if analysis.Selection.Reason == "" {
		t.Fatal("expected a reason")
	}
}

func TestVesselLimitTightensTheThreshold(t *testing.T) {
	cfg := config.Default()
	limits := LimitsFor(cfg, 1.2)
	if limits.MaxWaveHeightM != 1.2 {
		t.Fatalf("wave limit = %v, want the vessel limit", limits.MaxWaveHeightM)
	}
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	waves := []float64{1.5, 1.5, 1.0, 1.0, 1.0}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), limits, 2, model.UTCTime{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if analysis.Selection.Window.Start.String() != "2026-03-10T02:00:00Z" {
		t.Fatalf("window start = %s, want the calm stretch", analysis.Selection.Window.Start)
	}
}

func TestPerDaySummarySplitsAtMidnight(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T22:00:00Z")
	waves := []float64{1, 1, 1, 1}
	analysis, err := Analyze(cfg, "SYS-T", series("SYS-T", start, waves, nil), LimitsFor(cfg, 0), 2, model.UTCTime{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(analysis.Days) != 2 {
		t.Fatalf("days = %d, want 2", len(analysis.Days))
	}
	if analysis.Days[0].Date != "2026-03-10" || analysis.Days[1].Date != "2026-03-11" {
		t.Fatalf("dates = %s, %s", analysis.Days[0].Date, analysis.Days[1].Date)
	}
	if len(analysis.Windows) != 1 {
		t.Fatalf("the continuous window must not be split: %d", len(analysis.Windows))
	}
	if analysis.Days[0].LongestRunHours != 2 || analysis.Days[1].LongestRunHours != 2 {
		t.Fatalf("per-day runs = %v, %v", analysis.Days[0].LongestRunHours, analysis.Days[1].LongestRunHours)
	}
}

func TestAnalyzeRejectsUnknownSystemAndDuplicates(t *testing.T) {
	cfg := config.Default()
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	if _, err := Analyze(cfg, "SYS-MISSING", series("SYS-T", start, []float64{1}, nil), LimitsFor(cfg, 0), 1, model.UTCTime{}, false); err == nil {
		t.Fatal("expected an error when no observations match the system")
	}
	obs := series("SYS-T", start, []float64{1, 1}, nil)
	obs[1].HourStart = obs[0].HourStart
	if _, err := Analyze(cfg, "SYS-T", obs, LimitsFor(cfg, 0), 1, model.UTCTime{}, false); err == nil {
		t.Fatal("expected duplicate hour rejection")
	}
}

func TestLimitReasonsAreReported(t *testing.T) {
	cfg := config.Default()
	limits := LimitsFor(cfg, 0)
	obs := model.Observation{SystemID: "SYS-T", HourStart: model.MustParseUTC("2026-03-10T00:00:00Z"),
		WaveHeightM: 5, WindSpeedKn: 90, VisibilityKm: 0}
	workable, reason := limits.Workable(obs)
	if workable {
		t.Fatal("expected the hour to be unworkable")
	}
	if reason != "sea_state+visibility+wind" {
		t.Fatalf("reason = %q, want a sorted composite", reason)
	}
}
