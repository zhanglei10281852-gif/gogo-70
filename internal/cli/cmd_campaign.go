package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/campaign"
	"CableMend/internal/locate"
	"CableMend/internal/model"
	"CableMend/internal/store"
	"CableMend/internal/validate"
)

// CampaignOutput is the campaign command document.
type CampaignOutput struct {
	Campaign      campaign.Result `json:"campaign"`
	Localizations []locate.Result `json:"localizations"`
	SnapshotPath  string          `json:"snapshot_path,omitempty"`
	LedgerSeq     int             `json:"ledger_seq,omitempty"`
	AuditSeq      int             `json:"audit_seq,omitempty"`
	AuditHash     string          `json:"audit_hash,omitempty"`
	Stored        bool            `json:"stored"`
}

// cmdCampaign plans repairs for every declared fault.
func cmdCampaign(e env, args []string) error {
	var (
		c        common
		in       validate.Inputs
		asOfFlag string
		dryRun   bool
		withPlan bool
		fs       = flag.NewFlagSet("campaign", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.AssetsPath, "assets", "", "repair assets JSON document")
	fs.StringVar(&in.WeatherPath, "weather", "", "sea-state observations JSONL file")
	fs.StringVar(&in.PermitsPath, "permits", "", "permits JSON document")
	fs.StringVar(&in.FaultsPath, "faults", "", "faults JSON document")
	fs.StringVar(&in.EvidencePath, "evidence", "", "fault evidence JSONL file (default: the local store)")
	fs.StringVar(&asOfFlag, "as-of", "", "planning instant, RFC3339 UTC (default: latest input timestamp)")
	fs.BoolVar(&dryRun, "dry-run", false, "do not write a snapshot or ledger entry")
	fs.BoolVar(&withPlan, "plans", false, "embed the full stage plan of every assignment")
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
		"faults":  in.FaultsPath,
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
	if len(records) == 0 {
		return fmt.Errorf("no evidence records available for the campaign")
	}

	localizations := make(map[string]locate.Result, len(loaded.Faults))
	var ordered []locate.Result
	for _, fault := range loaded.Faults {
		ix, err := loaded.SystemSet.MustGet(fault.SystemID)
		if err != nil {
			return err
		}
		subset := model.FilterEvidence(records, fault.SystemID, fault.ID)
		if len(subset) == 0 {
			continue
		}
		res, err := locate.Localize(cfg, ix, subset, fault.ID)
		if err != nil {
			return err
		}
		localizations[fault.ID] = res
		ordered = append(ordered, res)
	}
	asOf, err := resolveAsOf(asOfFlag, records, loaded.Faults)
	if err != nil {
		return err
	}
	result, err := campaign.Plan(cfg, campaign.Request{
		Faults:        loaded.Faults,
		Localizations: localizations,
		Systems:       loaded.SystemSet,
		Assets:        loaded.Assets,
		Observations:  loaded.Observations,
		Permits:       loaded.Permits,
		AsOf:          asOf,
		IncludePlans:  withPlan,
	})
	if err != nil {
		return err
	}
	out := CampaignOutput{Campaign: result, Localizations: ordered}
	if !dryRun {
		st, err := c.openStore(cfg)
		if err != nil {
			return err
		}
		entry, audit, err := st.Commit(store.Event{
			Kind:     "campaign",
			At:       asOf,
			Subject:  fmt.Sprintf("%d faults", result.FaultCount),
			Summary:  fmt.Sprintf("%d assigned, %d unassigned", len(result.Assignments), len(result.Unassigned)),
			Snapshot: fmt.Sprintf("campaign-%s", asOf.String()),
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
		return renderCampaign(w, cfg.Decimals(), out)
	})
}

// renderCampaign prints a campaign result as text.
func renderCampaign(w io.Writer, decimals int, out CampaignOutput) error {
	r := out.Campaign
	t := newText(w, decimals)
	t.heading("repair campaign")
	t.kv("as of", r.AsOf.String())
	t.count("faults", r.FaultCount)
	t.count("assigned", len(r.Assignments))
	t.count("unassigned", len(r.Unassigned))
	t.num("total cost", r.TotalCost)
	t.num("total cable km", r.TotalCableKm)
	t.count("total joints", r.TotalJoints)
	t.num("makespan hours", r.MakespanHours)
	t.kv("first start", r.FirstStart.String())
	t.kv("last release", r.LastRelease.String())
	t.list("notes", r.Notes)
	t.blank()

	t.heading("priority order")
	priorityRows := make([][]string, 0, len(r.Priorities))
	for _, p := range r.Priorities {
		priorityRows = append(priorityRows, []string{
			fmt.Sprintf("%d", p.Rank), p.FaultID, p.SystemID,
			t.f(p.DeclaredPriority), t.f(p.TrafficTbps), boolWord(p.SingleRoute), t.f(p.Score),
		})
	}
	t.table([]string{"rank", "fault", "system", "declared", "tbps", "single route", "score"}, priorityRows)
	t.blank()

	t.heading("assignments")
	rows := make([][]string, 0, len(r.Assignments))
	for _, a := range r.Assignments {
		rows = append(rows, []string{
			fmt.Sprintf("%d", a.Order), a.FaultID, a.SystemID, a.VesselID, a.DepotID,
			a.StartAt.String(), a.EndAt.String(), t.f(a.TotalHours), t.f(a.Cost),
			t.f(a.RiskScore), boolWord(a.DeadlineMet),
		})
	}
	t.table([]string{"#", "fault", "system", "vessel", "depot", "start", "end", "hours", "cost", "risk", "deadline"}, rows)
	t.blank()

	if len(r.Unassigned) > 0 {
		t.heading("unassigned")
		unRows := make([][]string, 0, len(r.Unassigned))
		for _, u := range r.Unassigned {
			unRows = append(unRows, []string{
				fmt.Sprintf("%d", u.PriorityRank), u.FaultID, u.SystemID, t.f(u.Priority), joinOrDash(u.Reasons),
			})
		}
		t.table([]string{"rank", "fault", "system", "score", "reasons"}, unRows)
		t.blank()
	}

	t.heading("vessel utilization")
	vesselRows := make([][]string, 0, len(r.Vessels))
	for _, v := range r.Vessels {
		vesselRows = append(vesselRows, []string{
			v.VesselID, fmt.Sprintf("%d", v.Assignments), t.f(v.BusyHours),
			v.FirstStart.String(), v.LastRelease.String(), v.FinalSite.String(), joinOrDash(v.FaultIDs),
		})
	}
	t.table([]string{"vessel", "jobs", "busy h", "first start", "last release", "final site", "faults"}, vesselRows)
	t.blank()

	t.heading("depot draws")
	depotRows := make([][]string, 0, len(r.DepotDraws))
	for _, d := range r.DepotDraws {
		depotRows = append(depotRows, []string{d.DepotID, t.f(d.CableKm), fmt.Sprintf("%d", d.Joints), t.f(d.RemainingKm)})
	}
	t.table([]string{"depot", "cable km", "joints", "remaining km"}, depotRows)
	t.blank()

	t.heading("vessel contention")
	contentionRows := make([][]string, 0)
	for _, a := range r.Assignments {
		for _, con := range a.Contention {
			contentionRows = append(contentionRows, []string{
				a.FaultID, con.VesselID, con.BusyUntil.String(), con.FromFaultID,
			})
		}
	}
	t.table([]string{"fault", "vessel", "busy until", "released by"}, contentionRows)
	t.blank()

	t.heading("store")
	t.yesNo("stored", out.Stored)
	if out.Stored {
		t.kv("snapshot", out.SnapshotPath)
		t.count("ledger sequence", out.LedgerSeq)
		t.kv("audit head", out.AuditHash)
	}
	return t.done()
}
