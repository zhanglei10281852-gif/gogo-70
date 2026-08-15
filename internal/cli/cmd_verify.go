package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/quality"
	"CableMend/internal/store"
	"CableMend/internal/validate"
)

// VerifyOutput is the verify command document.
type VerifyOutput struct {
	Report       quality.Report `json:"report"`
	SnapshotPath string         `json:"snapshot_path,omitempty"`
	LedgerSeq    int            `json:"ledger_seq,omitempty"`
	AuditSeq     int            `json:"audit_seq,omitempty"`
	AuditHash    string         `json:"audit_hash,omitempty"`
	Stored       bool           `json:"stored"`
}

// cmdVerify checks post-repair measurements against the loss budget and joint
// limits and scores the residual risk.
func cmdVerify(e env, args []string) error {
	var (
		c      common
		in     validate.Inputs
		dryRun bool
		fs     = flag.NewFlagSet("verify", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&in.VerificationPath, "verification", "", "post-repair verification JSONL file")
	fs.BoolVar(&dryRun, "dry-run", false, "do not write a snapshot or ledger entry")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
	}
	if err := requireFlag("verification", in.VerificationPath); err != nil {
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
	report, err := quality.Verify(cfg, loaded.SystemSet, loaded.Verifications)
	if err != nil {
		return err
	}
	out := VerifyOutput{Report: report}
	if !dryRun {
		st, err := c.openStore(cfg)
		if err != nil {
			return err
		}
		entry, audit, err := st.Commit(store.Event{
			Kind:     "verify",
			At:       report.MeasuredTo,
			Subject:  fmt.Sprintf("%d records", report.RecordCount),
			Summary:  fmt.Sprintf("%d accepted, %d rejected", report.AcceptedCount, report.RejectedCount),
			Snapshot: fmt.Sprintf("verify-%s", report.MeasuredTo.String()),
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
	if err := c.emit(e, out, func(w io.Writer) error {
		return renderVerify(w, cfg.Decimals(), out)
	}); err != nil {
		return err
	}
	if report.RejectedCount > 0 {
		return fmt.Errorf("%d of %d verification records were rejected", report.RejectedCount, report.RecordCount)
	}
	return nil
}

// renderVerify prints a verification report as text.
func renderVerify(w io.Writer, decimals int, out VerifyOutput) error {
	r := out.Report
	t := newText(w, decimals)
	t.heading("post-repair verification")
	t.count("records", r.RecordCount)
	t.count("systems", r.SystemCount)
	t.count("accepted", r.AcceptedCount)
	t.count("rejected", r.RejectedCount)
	t.num("mean residual risk", r.MeanResidualRisk)
	t.kv("worst record", fmt.Sprintf("%s (%s)", r.WorstRecordID, t.f(r.WorstResidual)))
	t.kv("measured", r.MeasuredFrom.String()+" .. "+r.MeasuredTo.String())
	t.blank()

	t.heading("checks")
	rows := make([][]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		rows = append(rows, []string{
			check.RecordID,
			check.SystemID,
			check.SegmentID,
			t.f(check.MeasuredLossDb),
			t.f(check.ExpectedLossDb),
			t.f(check.DeltaDb),
			boolWord(check.LossWithinTolerance),
			fmt.Sprintf("%d/%d", check.JointsAfterRepair, check.MaxJoints),
			t.f(check.BurialDeficitM),
			t.f(check.ResidualRisk),
			check.RiskBand,
			boolWord(check.Accepted),
		})
	}
	t.table([]string{"record", "system", "segment", "measured db", "expected db", "delta db", "loss ok",
		"joints", "burial deficit m", "risk", "band", "accepted"}, rows)
	t.blank()

	t.heading("findings")
	findingRows := make([][]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		findingRows = append(findingRows, []string{f.Finding, fmt.Sprintf("%d", f.Count)})
	}
	t.table([]string{"finding", "count"}, findingRows)
	t.blank()

	t.heading("per record findings")
	for _, check := range r.Checks {
		t.list(check.RecordID, check.Findings)
	}
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
