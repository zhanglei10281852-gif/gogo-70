package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"CableMend/internal/model"
	"CableMend/internal/store"
)

// MethodCount counts evidence records per measurement method.
type MethodCount struct {
	Method string `json:"method"`
	Count  int    `json:"count"`
}

// SystemEvidenceStat summarizes the evidence held for one system.
type SystemEvidenceStat struct {
	SystemID     string        `json:"system_id"`
	Records      int           `json:"records"`
	Faults       []string      `json:"faults"`
	ObservedFrom model.UTCTime `json:"observed_from"`
	ObservedTo   model.UTCTime `json:"observed_to"`
	Methods      []MethodCount `json:"methods"`
	Ends         []MethodCount `json:"ends"`
}

// EvidenceStats is the evidence section of the store report.
type EvidenceStats struct {
	Records int                  `json:"records"`
	Faults  int                  `json:"faults"`
	Systems []SystemEvidenceStat `json:"systems"`
	Methods []MethodCount        `json:"methods"`
}

// ReportOutput is the report command document.
type ReportOutput struct {
	StoreDir  string                  `json:"store_dir"`
	Meta      store.Meta              `json:"meta"`
	Evidence  EvidenceStats           `json:"evidence"`
	Ledger    []store.LedgerEntry     `json:"ledger"`
	Audit     store.ChainVerification `json:"audit"`
	Snapshots []string                `json:"snapshots"`
}

// cmdReport summarizes the local store and verifies the audit chain.
func cmdReport(e env, args []string) error {
	var (
		c  common
		fs = flag.NewFlagSet("report", flag.ContinueOnError)
	)
	c.register(fs)
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	cfg, err := c.loadConfig()
	if err != nil {
		return err
	}
	st, err := c.openStore(cfg)
	if err != nil {
		return err
	}
	meta, err := st.ReadMeta()
	if err != nil {
		return err
	}
	evidence, err := st.LoadEvidence()
	if err != nil {
		return err
	}
	ledger, err := st.LoadLedger()
	if err != nil {
		return err
	}
	chain, err := st.VerifyAudit()
	if err != nil {
		return err
	}
	snapshots, err := listSnapshots(st)
	if err != nil {
		return err
	}
	out := ReportOutput{
		StoreDir:  st.Root(),
		Meta:      meta,
		Evidence:  summarizeEvidence(evidence),
		Ledger:    ledger,
		Audit:     chain,
		Snapshots: snapshots,
	}
	if err := c.emit(e, out, func(w io.Writer) error {
		return renderReport(w, cfg.Decimals(), out)
	}); err != nil {
		return err
	}
	if !chain.Valid {
		return fmt.Errorf("audit chain verification failed with %d problems", len(chain.Problems))
	}
	return nil
}

// listSnapshots returns the snapshot file names in sorted order.
func listSnapshots(st *store.Store) ([]string, error) {
	dir := filepath.Join(st.Root(), store.SnapshotDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshots: %w", err)
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out, nil
}

// summarizeEvidence aggregates the evidence log.
func summarizeEvidence(records []model.Evidence) EvidenceStats {
	stats := EvidenceStats{Records: len(records)}
	systems, bySystem := model.GroupEvidenceBySystem(records)
	faults, _ := model.GroupEvidenceByFault(records)
	stats.Faults = len(faults)
	methodTotals := map[string]int{}
	for _, id := range systems {
		group := bySystem[id]
		entry := SystemEvidenceStat{SystemID: id, Records: len(group)}
		faultSet := map[string]bool{}
		methods := map[string]int{}
		ends := map[string]int{}
		var times []model.UTCTime
		for _, rec := range group {
			faultSet[rec.FaultID] = true
			methods[string(rec.Method)]++
			methodTotals[string(rec.Method)]++
			ends[string(rec.End)]++
			times = append(times, rec.ObservedAt)
		}
		for fault := range faultSet {
			entry.Faults = append(entry.Faults, fault)
		}
		sort.Strings(entry.Faults)
		entry.Methods = countsOf(methods)
		entry.Ends = countsOf(ends)
		entry.ObservedFrom = model.EarliestTime(times)
		entry.ObservedTo = model.LatestTime(times)
		stats.Systems = append(stats.Systems, entry)
	}
	stats.Methods = countsOf(methodTotals)
	return stats
}

// countsOf renders a count map as a sorted slice.
func countsOf(counts map[string]int) []MethodCount {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]MethodCount, 0, len(keys))
	for _, k := range keys {
		out = append(out, MethodCount{Method: k, Count: counts[k]})
	}
	return out
}

// renderReport prints the store report as text.
func renderReport(w io.Writer, decimals int, out ReportOutput) error {
	t := newText(w, decimals)
	t.heading("store report")
	t.kv("store", out.StoreDir)
	t.count("evidence records", out.Meta.EvidenceCount)
	t.count("ledger entries", out.Meta.LedgerCount)
	t.count("audit records", out.Meta.AuditCount)
	t.count("snapshots", out.Meta.SnapshotCount)
	t.list("systems", out.Meta.Systems)
	t.list("faults", out.Meta.Faults)
	t.kv("last event", out.Meta.LastEventAt.String()+" "+orDash(out.Meta.LastEventKind))
	t.kv("audit head", orDash(out.Meta.LastAuditHash))
	t.blank()

	t.heading("evidence by system")
	rows := make([][]string, 0, len(out.Evidence.Systems))
	for _, entry := range out.Evidence.Systems {
		methods := make([]string, 0, len(entry.Methods))
		for _, m := range entry.Methods {
			methods = append(methods, fmt.Sprintf("%s=%d", m.Method, m.Count))
		}
		ends := make([]string, 0, len(entry.Ends))
		for _, m := range entry.Ends {
			ends = append(ends, fmt.Sprintf("%s=%d", m.Method, m.Count))
		}
		rows = append(rows, []string{
			entry.SystemID,
			fmt.Sprintf("%d", entry.Records),
			joinOrDash(entry.Faults),
			entry.ObservedFrom.String(),
			entry.ObservedTo.String(),
			joinOrDash(methods),
			joinOrDash(ends),
		})
	}
	t.table([]string{"system", "records", "faults", "from", "to", "methods", "ends"}, rows)
	t.blank()

	t.heading("work ledger")
	ledgerRows := make([][]string, 0, len(out.Ledger))
	for _, entry := range out.Ledger {
		ledgerRows = append(ledgerRows, []string{
			fmt.Sprintf("%d", entry.Seq),
			entry.Kind,
			entry.At.String(),
			entry.Subject,
			entry.Summary,
			orDash(entry.SnapshotPath),
		})
	}
	t.table([]string{"seq", "kind", "at", "subject", "summary", "snapshot"}, ledgerRows)
	t.blank()

	t.heading("audit chain")
	t.count("records", out.Audit.Records)
	t.count("ledger entries", out.Audit.LedgerEntries)
	t.yesNo("valid", out.Audit.Valid)
	t.kv("head hash", orDash(out.Audit.HeadHash))
	if len(out.Audit.Problems) > 0 {
		problemRows := make([][]string, 0, len(out.Audit.Problems))
		for _, p := range out.Audit.Problems {
			problemRows = append(problemRows, []string{fmt.Sprintf("%d", p.Seq), p.Kind, p.Detail})
		}
		t.table([]string{"seq", "problem", "detail"}, problemRows)
	}
	if len(out.Audit.Notes) > 0 {
		noteRows := make([][]string, 0, len(out.Audit.Notes))
		for _, n := range out.Audit.Notes {
			noteRows = append(noteRows, []string{fmt.Sprintf("%d", n.Seq), n.Kind, n.Detail})
		}
		t.table([]string{"seq", "note", "detail"}, noteRows)
	}
	t.blank()

	t.heading("snapshots")
	if len(out.Snapshots) == 0 {
		t.line("  (none)")
	}
	for _, name := range out.Snapshots {
		t.line("  %s", name)
	}
	return t.done()
}
