package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/validate"
)

// LocateOutput is the locate command document.
type LocateOutput struct {
	Source        string          `json:"source"`
	SystemID      string          `json:"system_filter,omitempty"`
	FaultID       string          `json:"fault_filter,omitempty"`
	Count         int             `json:"count"`
	Localizations []locate.Result `json:"localizations"`
}

// cmdLocate localizes one or more faults.
func cmdLocate(e env, args []string) error {
	var (
		c            common
		in           validate.Inputs
		systemFilter string
		faultFilter  string
		detail       bool
		fs           = flag.NewFlagSet("locate", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.EvidencePath, "evidence", "", "fault evidence JSONL file (default: the local store)")
	fs.StringVar(&systemFilter, "system", "", "restrict localization to one system id")
	fs.StringVar(&faultFilter, "fault", "", "restrict localization to one fault id")
	fs.BoolVar(&detail, "detail", true, "include the per-measurement breakdown in text output")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
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
	source := in.EvidencePath
	records := loaded.Evidence
	if in.EvidencePath == "" {
		st, err := c.openStore(cfg)
		if err != nil {
			return err
		}
		stored, err := st.LoadEvidence()
		if err != nil {
			return err
		}
		records = stored
		source = st.Path("evidence.jsonl")
	}
	records = model.FilterEvidence(records, systemFilter, faultFilter)
	if len(records) == 0 {
		return fmt.Errorf("no evidence records match the requested filters")
	}
	results, err := locate.LocalizeAll(cfg, loaded.SystemSet, records)
	if err != nil {
		return err
	}
	out := LocateOutput{
		Source:        source,
		SystemID:      systemFilter,
		FaultID:       faultFilter,
		Count:         len(results),
		Localizations: results,
	}
	return c.emit(e, out, func(w io.Writer) error {
		return renderLocate(w, cfg.Decimals(), out, detail)
	})
}

// renderLocate prints localization results as text.
func renderLocate(w io.Writer, decimals int, out LocateOutput, detail bool) error {
	t := newText(w, decimals)
	t.heading("fault localization")
	t.kv("evidence source", out.Source)
	t.count("localizations", out.Count)
	t.blank()

	for _, res := range out.Localizations {
		t.heading(fmt.Sprintf("%s / %s", res.SystemID, res.FaultID))
		t.kv("system name", res.SystemName)
		t.count("evidence records", res.EvidenceCount)
		t.kv("observed", res.ObservedFrom.String()+" .. "+res.ObservedTo.String())
		t.num("best estimate KP", res.BestKP)
		t.kv("window KP", fmt.Sprintf("%s .. %s", t.f(res.WindowLowKP), t.f(res.WindowHighKP)))
		t.num("window width km", res.WindowWidthKm)
		t.num("uncertainty km", res.UncertaintyKm)
		t.list("ends used", res.EndsUsed)
		t.num("end disagreement km", res.EndDisagreementKm)
		t.num("tolerance km", res.ToleranceKm)
		t.yesNo("consistent", res.Consistent)
		t.kv("fault class", string(res.FaultClass))
		t.num("loss step total db", res.LossStepTotalDb)
		t.kv("segment", fmt.Sprintf("%s (%s, slack %s)", res.SegmentID, res.SegmentCableType, t.fixed(res.SegmentSlack, 4)))
		t.list("protection zones", zoneLabels(res.Zones))
		t.list("jurisdictions", res.Jurisdictions)
		if res.Depth.Known {
			t.kv("depth m", fmt.Sprintf("%s (burial design %s m)", t.f(res.Depth.DepthM), t.f(res.Depth.BurialDepthM)))
		} else {
			t.kv("depth m", "unknown")
		}
		t.num("buried length km", res.BuriedLengthKm)
		if res.NearestInlineID != "" {
			t.kv("nearest inline asset", fmt.Sprintf("%s (%s) at %s km", res.NearestInlineID, res.NearestInlineKind, t.f(res.NearestInlineKm)))
		}
		t.kv("cable km from ends", fmt.Sprintf("A %s / B %s", t.f(res.CableKmFromA), t.f(res.CableKmFromB)))
		t.list("flags", res.Flags)
		t.list("advice", res.Advice)
		t.blank()

		if detail {
			rows := make([][]string, 0, len(res.Estimates))
			for _, est := range res.Estimates {
				rows = append(rows, []string{
					est.EvidenceID,
					est.End,
					est.Method,
					t.f(est.MeasuredCableKm),
					t.f(est.RouteKP),
					t.f(est.UncertaintyKm),
					t.f(est.DeviationKm),
					string(est.FaultClass),
					joinOrDash(est.Flags),
				})
			}
			t.table([]string{"evidence", "end", "method", "cable km", "route kp", "sigma km", "deviation", "class", "flags"}, rows)
			t.blank()
		}

		votes := make([]string, 0, len(res.ClassVotes))
		for _, vote := range res.ClassVotes {
			votes = append(votes, fmt.Sprintf("%s=%d", vote.Class, vote.Count))
		}
		t.list("class votes", votes)
		t.blank()
	}
	return t.done()
}

// zoneLabels renders zone references compactly.
func zoneLabels(zones []locate.ZoneRef) []string {
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		permit := "no permit"
		if z.PermitRequired {
			permit = "permit required"
		}
		out = append(out, fmt.Sprintf("%s [%s] %s", z.ID, z.Jurisdiction, permit))
	}
	return out
}
