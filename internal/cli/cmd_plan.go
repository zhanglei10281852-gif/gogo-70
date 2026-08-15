package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/planner"
	"CableMend/internal/store"
	"CableMend/internal/validate"
)

// PlanOutput is the plan command document.
type PlanOutput struct {
	Plan         planner.Plan  `json:"plan"`
	Localization locate.Result `json:"localization"`
	SnapshotPath string        `json:"snapshot_path,omitempty"`
	LedgerSeq    int           `json:"ledger_seq,omitempty"`
	AuditSeq     int           `json:"audit_seq,omitempty"`
	AuditHash    string        `json:"audit_hash,omitempty"`
	Stored       bool          `json:"stored"`
}

// cmdPlan builds a repair plan for a single fault.
func cmdPlan(e env, args []string) error {
	var (
		c        common
		in       validate.Inputs
		systemID string
		faultID  string
		asOfFlag string
		dryRun   bool
		showAll  bool
		fs       = flag.NewFlagSet("plan", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.AssetsPath, "assets", "", "repair assets JSON document")
	fs.StringVar(&in.WeatherPath, "weather", "", "sea-state observations JSONL file")
	fs.StringVar(&in.PermitsPath, "permits", "", "permits JSON document")
	fs.StringVar(&in.EvidencePath, "evidence", "", "fault evidence JSONL file (default: the local store)")
	fs.StringVar(&systemID, "system", "", "system id to plan for")
	fs.StringVar(&faultID, "fault", "", "fault id to plan for")
	fs.StringVar(&asOfFlag, "as-of", "", "planning instant, RFC3339 UTC (default: latest input timestamp)")
	fs.BoolVar(&dryRun, "dry-run", false, "do not write a snapshot or ledger entry")
	fs.BoolVar(&showAll, "candidates", false, "list every evaluated vessel and depot pairing")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"systems": in.SystemsPath,
		"assets":  in.AssetsPath,
		"weather": in.WeatherPath,
		"system":  systemID,
	} {
		if err := requireFlag(name, value); err != nil {
			return err
		}
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
	ix, err := loaded.SystemSet.MustGet(systemID)
	if err != nil {
		return err
	}

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
	}
	records = model.FilterEvidence(records, systemID, faultID)
	if len(records) == 0 {
		return fmt.Errorf("no evidence records for system %q", systemID)
	}
	resolvedFault := faultID
	if resolvedFault == "" {
		faults, _ := model.GroupEvidenceByFault(records)
		if len(faults) != 1 {
			return fmt.Errorf("evidence covers %d faults; pass --fault to choose one", len(faults))
		}
		resolvedFault = faults[0]
	}
	res, err := locate.Localize(cfg, ix, records, resolvedFault)
	if err != nil {
		return err
	}
	asOf, err := resolveAsOf(asOfFlag, records, loaded.Faults)
	if err != nil {
		return err
	}
	fault := model.FaultRecord{ID: resolvedFault, SystemID: systemID, ReportedAt: res.ObservedFrom}
	for _, candidate := range loaded.Faults {
		if candidate.ID == resolvedFault {
			fault = candidate
			break
		}
	}
	plan, err := planner.Build(cfg, planner.Request{
		Fault:        fault,
		Localization: res,
		Index:        ix,
		Assets:       loaded.AssetSet,
		Observations: loaded.Observations,
		Permits:      loaded.Permits,
		AsOf:         asOf,
	})
	if err != nil {
		return err
	}
	out := PlanOutput{Plan: plan, Localization: res}
	if !dryRun {
		st, err := c.openStore(cfg)
		if err != nil {
			return err
		}
		entry, audit, err := st.Commit(store.Event{
			Kind:     "plan",
			At:       asOf,
			Subject:  fmt.Sprintf("%s/%s", systemID, resolvedFault),
			Summary:  planSummary(plan),
			Snapshot: fmt.Sprintf("plan-%s-%s", systemID, resolvedFault),
			Payload:  out,
		})
		if err != nil {
			return err
		}
		out.SnapshotPath = entry.SnapshotPath
		out.LedgerSeq = entry.Seq
		out.AuditSeq = audit.Seq
		out.AuditHash = audit.Hash
		out.Stored = true
	}
	return c.emit(e, out, func(w io.Writer) error {
		return renderPlan(w, cfg.Decimals(), out, showAll)
	})
}

// planSummary renders a one-line ledger summary.
func planSummary(p planner.Plan) string {
	if !p.Feasible {
		return "no feasible vessel and depot pairing"
	}
	return fmt.Sprintf("vessel %s from depot %s, on site %s", p.VesselID, p.DepotID, p.Window.Start)
}

// renderPlan prints a repair plan as text.
func renderPlan(w io.Writer, decimals int, out PlanOutput, showAll bool) error {
	p := out.Plan
	t := newText(w, decimals)
	t.heading("repair plan")
	t.kv("fault", p.FaultID)
	t.kv("system", fmt.Sprintf("%s (%s)", p.SystemID, p.SystemName))
	t.kv("as of", p.AsOf.String())
	t.yesNo("feasible", p.Feasible)
	t.kv("site", fmt.Sprintf("KP %s", t.f(p.Site.KP)))
	t.kv("localization window KP", fmt.Sprintf("%s .. %s", t.f(p.Localization.WindowLowKP), t.f(p.Localization.WindowHighKP)))
	t.kv("fault class", string(p.Localization.FaultClass))
	t.kv("segment", p.SegmentID)
	t.num("burial length km", p.BurialLengthKm)
	t.list("operations", p.Operations)
	if !p.Feasible {
		t.list("blocking reasons", p.Reasons)
		t.list("notes", p.Notes)
		t.blank()
		if showAll {
			renderCandidates(t, p.Candidates)
		}
		return t.done()
	}

	t.kv("vessel", fmt.Sprintf("%s (%s)", p.VesselID, p.VesselName))
	t.kv("depot", fmt.Sprintf("%s (%s)", p.DepotID, p.DepotName))
	t.num("selection cost", p.Cost)
	t.num("total hours", p.TotalHours)
	t.num("on site hours", p.OnSiteHours)
	t.kv("schedule", p.StartAt.String()+" .. "+p.EndAt.String())
	t.kv("work window", p.Window.Start.String()+" .. "+p.Window.End.String())
	t.num("window max wave m", p.Window.MaxWaveHeightM)
	t.yesNo("deadline met", p.DeadlineMet)
	if !p.RestoreBy.IsZero() {
		t.kv("restore by", p.RestoreBy.String())
	}
	t.num("risk score", p.RiskScore)
	t.list("risk flags", p.RiskFlags)
	t.list("notes", p.Notes)
	t.blank()

	t.heading("stages")
	rows := make([][]string, 0, len(p.Stages))
	for _, stage := range p.Stages {
		rows = append(rows, []string{
			fmt.Sprintf("%d", stage.Order),
			stage.Name,
			orDash(stage.Operation),
			stage.StartAt.String(),
			stage.EndAt.String(),
			t.f(stage.Hours),
			t.f(stage.CumulativeHours),
			boolWord(stage.OnSite),
			orDash(stage.Note),
		})
	}
	t.table([]string{"#", "stage", "operation", "start", "end", "hours", "cumulative", "on site", "note"}, rows)
	t.blank()

	t.heading("spares")
	s := p.Spares
	t.num("replaced route km", s.ReplacedRouteKm)
	t.kv("segment slack", t.fixed(s.SegmentSlack, 4))
	t.num("base cable km", s.BaseCableKm)
	t.num("bight allowance km", s.BightAllowanceKm)
	t.num("slack allowance km", s.SlackAllowanceKm)
	t.num("total cable km", s.TotalCableKm)
	t.count("joints", s.Joints)
	t.num("from vessel km", s.FromVesselKm)
	t.num("from depot km", s.FromDepotKm)
	t.num("shortfall km", s.ShortfallKm)
	t.yesNo("sufficient", s.Sufficient)
	t.blank()

	t.heading("permits")
	t.yesNo("satisfied", p.Permits.Satisfied)
	t.list("used permits", p.Permits.UsedPermitIDs)
	permitRows := make([][]string, 0, len(p.Permits.Requirements))
	for _, req := range p.Permits.Requirements {
		permitRows = append(permitRows, []string{
			req.ZoneID, req.Jurisdiction, boolWord(req.PermitRequired), boolWord(req.Satisfied),
			orDash(req.CoveringPermitID), orDash(req.Note),
		})
	}
	t.table([]string{"zone", "jurisdiction", "required", "satisfied", "permit", "note"}, permitRows)
	t.blank()

	t.heading("cost breakdown")
	costRows := make([][]string, 0, len(p.CostItems))
	for _, item := range p.CostItems {
		costRows = append(costRows, []string{item.Name, t.f(item.Amount), orDash(item.Detail)})
	}
	t.table([]string{"item", "amount", "detail"}, costRows)
	t.blank()

	if showAll {
		renderCandidates(t, p.Candidates)
		t.blank()
	}

	t.heading("store")
	t.yesNo("stored", out.Stored)
	if out.Stored {
		t.kv("snapshot", out.SnapshotPath)
		t.count("ledger sequence", out.LedgerSeq)
		t.kv("audit head", out.AuditHash)
	}
	return t.done()
}

// renderCandidates prints the evaluated pairings.
func renderCandidates(t *textWriter, candidates []planner.Candidate) {
	t.heading("candidates")
	rows := make([][]string, 0, len(candidates))
	for _, cand := range candidates {
		rows = append(rows, []string{
			cand.VesselID,
			cand.DepotID,
			boolWord(cand.Feasible),
			t.f(cand.TransitNm),
			t.f(cand.TransitHours),
			t.f(cand.StandbyHours),
			t.f(cand.TotalHours),
			t.f(cand.Cost),
			joinOrDash(cand.Reasons),
		})
	}
	t.table([]string{"vessel", "depot", "feasible", "nm", "transit h", "standby h", "total h", "cost", "reasons"}, rows)
}
