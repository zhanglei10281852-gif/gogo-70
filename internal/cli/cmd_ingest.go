package cli

import (
	"flag"
	"fmt"
	"io"
	"sort"

	"CableMend/internal/model"
	"CableMend/internal/store"
	"CableMend/internal/validate"
)

// IngestSummary is the ingest output document.
type IngestSummary struct {
	StoreDir       string             `json:"store_dir"`
	EvidencePath   string             `json:"evidence_path"`
	Appended       int                `json:"appended"`
	Duplicates     int                `json:"duplicates"`
	TotalInStore   int                `json:"total_in_store"`
	NewIDs         []string           `json:"new_ids"`
	Systems        []string           `json:"systems"`
	Faults         []string           `json:"faults"`
	ObservedFrom   model.UTCTime      `json:"observed_from"`
	ObservedTo     model.UTCTime      `json:"observed_to"`
	LedgerSeq      int                `json:"ledger_seq"`
	AuditSeq       int                `json:"audit_seq"`
	AuditHash      string             `json:"audit_hash"`
	PayloadHash    string             `json:"payload_sha256"`
	ChainVerified  bool               `json:"chain_verified"`
	PerSystemCount []SystemCountEntry `json:"per_system_count"`
}

// SystemCountEntry is a per-system record count.
type SystemCountEntry struct {
	SystemID string `json:"system_id"`
	Records  int    `json:"records"`
}

// cmdIngest appends evidence to the append-only store and records the event in
// the ledger and audit chain.
func cmdIngest(e env, args []string) error {
	var (
		c  common
		in validate.Inputs
		fs = flag.NewFlagSet("ingest", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.EvidencePath, "evidence", "", "fault evidence JSONL file")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
	}
	if err := requireFlag("evidence", in.EvidencePath); err != nil {
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
	if len(loaded.Evidence) == 0 {
		return fmt.Errorf("%s contains no evidence records", in.EvidencePath)
	}
	st, err := c.openStore(cfg)
	if err != nil {
		return err
	}
	appended, err := st.AppendEvidence(loaded.Evidence)
	if err != nil {
		return err
	}

	systems, bySystem := model.GroupEvidenceBySystem(loaded.Evidence)
	faults, _ := model.GroupEvidenceByFault(loaded.Evidence)
	var times []model.UTCTime
	for _, rec := range loaded.Evidence {
		times = append(times, rec.ObservedAt)
	}
	summary := IngestSummary{
		StoreDir:     st.Root(),
		EvidencePath: in.EvidencePath,
		Appended:     appended.Appended,
		Duplicates:   appended.Duplicate,
		TotalInStore: appended.Total,
		NewIDs:       appended.NewIDs,
		Systems:      systems,
		Faults:       faults,
		ObservedFrom: model.EarliestTime(times),
		ObservedTo:   model.LatestTime(times),
	}
	for _, id := range systems {
		summary.PerSystemCount = append(summary.PerSystemCount, SystemCountEntry{SystemID: id, Records: len(bySystem[id])})
	}
	sort.SliceStable(summary.PerSystemCount, func(i, j int) bool {
		return summary.PerSystemCount[i].SystemID < summary.PerSystemCount[j].SystemID
	})

	entry, audit, err := st.Commit(store.Event{
		Kind:    "ingest",
		At:      summary.ObservedTo,
		Subject: subjectOf(systems),
		Summary: fmt.Sprintf("appended %d of %d evidence records", appended.Appended, len(loaded.Evidence)),
		Payload: summary,
	})
	if err != nil {
		return err
	}
	summary.LedgerSeq = entry.Seq
	summary.AuditSeq = audit.Seq
	summary.AuditHash = audit.Hash
	summary.PayloadHash = entry.PayloadHash
	chain, err := st.VerifyAudit()
	if err != nil {
		return err
	}
	summary.ChainVerified = chain.Valid

	return c.emit(e, summary, func(w io.Writer) error {
		return renderIngest(w, cfg.Decimals(), summary)
	})
}

// subjectOf renders a ledger subject from a set of system ids.
func subjectOf(systems []string) string {
	switch len(systems) {
	case 0:
		return "none"
	case 1:
		return systems[0]
	default:
		return fmt.Sprintf("%d systems", len(systems))
	}
}

// renderIngest prints the ingest summary as text.
func renderIngest(w io.Writer, decimals int, s IngestSummary) error {
	t := newText(w, decimals)
	t.heading("evidence ingest")
	t.kv("store", s.StoreDir)
	t.kv("source", s.EvidencePath)
	t.count("appended", s.Appended)
	t.count("duplicates skipped", s.Duplicates)
	t.count("records in store", s.TotalInStore)
	t.kv("observed from", s.ObservedFrom.String())
	t.kv("observed to", s.ObservedTo.String())
	t.list("systems", s.Systems)
	t.list("faults", s.Faults)
	t.blank()

	t.heading("per system")
	rows := make([][]string, 0, len(s.PerSystemCount))
	for _, entry := range s.PerSystemCount {
		rows = append(rows, []string{entry.SystemID, fmt.Sprintf("%d", entry.Records)})
	}
	t.table([]string{"system", "records"}, rows)
	t.blank()

	t.heading("ledger")
	t.count("ledger sequence", s.LedgerSeq)
	t.count("audit sequence", s.AuditSeq)
	t.kv("payload sha256", s.PayloadHash)
	t.kv("audit head", s.AuditHash)
	t.yesNo("chain verified", s.ChainVerified)
	return t.done()
}
