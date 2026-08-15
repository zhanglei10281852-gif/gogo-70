package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/model"
	"CableMend/internal/validate"
	"CableMend/internal/weather"
)

// cmdWindow computes workability windows for one system.
func cmdWindow(e env, args []string) error {
	var (
		c          common
		in         validate.Inputs
		systemID   string
		hours      float64
		maxWave    float64
		maxWind    float64
		minVis     float64
		notBefore  string
		vesselID   string
		showHourly bool
		fs         = flag.NewFlagSet("window", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.WeatherPath, "weather", "", "sea-state observations JSONL file")
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.AssetsPath, "assets", "", "repair assets JSON document, used with --vessel")
	fs.StringVar(&systemID, "system", "", "system id to analyse")
	fs.Float64Var(&hours, "hours", 24, "required consecutive workable hours")
	fs.Float64Var(&maxWave, "max-wave", 0, "override the maximum significant wave height in metres")
	fs.Float64Var(&maxWind, "max-wind", 0, "override the maximum wind speed in knots")
	fs.Float64Var(&minVis, "min-visibility", -1, "override the minimum visibility in kilometres")
	fs.StringVar(&notBefore, "not-before", "", "earliest acceptable window start, RFC3339 UTC")
	fs.StringVar(&vesselID, "vessel", "", "apply this vessel's sea-state limit")
	fs.BoolVar(&showHourly, "hourly", false, "include the classified hourly series")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("weather", in.WeatherPath); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
	}
	if err := requireFlag("system", systemID); err != nil {
		return err
	}
	if vesselID != "" && in.AssetsPath == "" {
		return fmt.Errorf("--assets is required when --vessel is given")
	}
	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	in.ConfigPath = c.configPath
	loaded, err := loadInputs(cfg, in)
	if err != nil {
		return err
	}
	if _, err := loaded.SystemSet.MustGet(systemID); err != nil {
		return err
	}

	limits := weather.LimitsFor(cfg, 0)
	if vesselID != "" {
		vessel, ok := loaded.AssetSet.Vessel(vesselID)
		if !ok {
			return fmt.Errorf("unknown vessel %q", vesselID)
		}
		limits = weather.LimitsFor(cfg, vessel.MaxSeaStateM)
	}
	if maxWave > 0 {
		limits.MaxWaveHeightM = maxWave
	}
	if maxWind > 0 {
		limits.MaxWindKn = maxWind
	}
	if minVis >= 0 {
		limits.MinVisibilityKm = minVis
	}

	var start model.UTCTime
	if notBefore != "" {
		start, err = model.ParseUTC(notBefore)
		if err != nil {
			return err
		}
	}
	analysis, err := weather.Analyze(cfg, systemID, loaded.Observations, limits, hours, start, showHourly)
	if err != nil {
		return err
	}
	return c.emit(e, analysis, func(w io.Writer) error {
		return renderWindow(w, cfg.Decimals(), analysis, showHourly)
	})
}

// renderWindow prints a workability analysis as text.
func renderWindow(w io.Writer, decimals int, a weather.Analysis, showHourly bool) error {
	t := newText(w, decimals)
	t.heading("workability analysis")
	t.kv("system", a.SystemID)
	t.count("observations", a.ObservationCount)
	t.kv("coverage", a.CoverageFrom.String()+" .. "+a.CoverageTo.String())
	t.num("observed hours", a.ObservationHours)
	t.num("workable hours", a.WorkableHours)
	t.num("limit wave m", a.Limits.MaxWaveHeightM)
	t.num("limit wind kn", a.Limits.MaxWindKn)
	t.num("limit visibility km", a.Limits.MinVisibilityKm)
	t.blank()

	t.heading("per day")
	rows := make([][]string, 0, len(a.Days))
	for _, day := range a.Days {
		reasons := make([]string, 0, len(day.LimitingReasons))
		for _, r := range day.LimitingReasons {
			reasons = append(reasons, fmt.Sprintf("%s %s h", r.Reason, t.f(r.Hours)))
		}
		rows = append(rows, []string{
			day.Date,
			t.f(day.ObservedHours),
			t.f(day.WorkableHours),
			t.f(day.LongestRunHours),
			fmt.Sprintf("%d", len(day.Windows)),
			joinOrDash(reasons),
		})
	}
	t.table([]string{"date", "observed h", "workable h", "longest h", "windows", "limiting"}, rows)
	t.blank()

	t.heading("continuous windows")
	windowRows := make([][]string, 0, len(a.Windows))
	for _, win := range a.Windows {
		windowRows = append(windowRows, []string{
			win.Start.String(), win.End.String(), t.f(win.Hours),
			t.f(win.MaxWaveHeightM), t.f(win.MaxWindKn), t.f(win.MinVisibilityKm),
		})
	}
	t.table([]string{"start", "end", "hours", "max wave m", "max wind kn", "min vis km"}, windowRows)
	t.blank()

	if len(a.Gaps) > 0 {
		t.heading("observation gaps")
		gapRows := make([][]string, 0, len(a.Gaps))
		for _, gap := range a.Gaps {
			gapRows = append(gapRows, []string{gap.After.String(), gap.Before.String(), t.f(gap.Hours)})
		}
		t.table([]string{"after", "before", "missing h"}, gapRows)
		t.blank()
	}

	t.heading("selection")
	t.num("required hours", a.Selection.RequiredHours)
	t.yesNo("found", a.Selection.Found)
	if a.Selection.Found {
		t.kv("window", a.Selection.Window.Start.String()+" .. "+a.Selection.Window.End.String())
		t.kv("host run", a.Selection.RunStart.String()+" .. "+a.Selection.RunEnd.String())
		t.num("host run hours", a.Selection.RunHours)
		t.num("wait hours", a.Selection.WaitHours)
		t.num("window max wave m", a.Selection.Window.MaxWaveHeightM)
		t.num("window max wind kn", a.Selection.Window.MaxWindKn)
	} else {
		t.kv("reason", a.Selection.Reason)
	}

	if showHourly {
		t.blank()
		t.heading("hourly series")
		hourRows := make([][]string, 0, len(a.Hours))
		for _, h := range a.Hours {
			hourRows = append(hourRows, []string{
				h.Start.String(), boolWord(h.Workable), t.f(h.WaveHeightM), t.f(h.WindSpeedKn), t.f(h.VisibilityKm),
				orDash(h.Reason),
			})
		}
		t.table([]string{"hour", "workable", "wave m", "wind kn", "vis km", "reason"}, hourRows)
	}
	return t.done()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
