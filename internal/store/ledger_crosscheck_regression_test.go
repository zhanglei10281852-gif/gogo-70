package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"CableMend/internal/model"
)

// TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain pins the store verdict
// against both halves of the integrity check: a work ledger whose payload no
// longer matches the audit record it is linked to must make verification fail.
func TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	st, err := Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i, at := range []string{"2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"} {
		if _, _, err := st.Commit(Event{
			Kind: "plan", At: model.MustParseUTC(at), Subject: "SYS-T",
			Summary: "entry", Payload: map[string]int{"index": i},
		}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	clean, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !clean.Valid || len(clean.Problems) != 0 {
		t.Fatalf("a freshly written store must verify: %+v", clean)
	}
	if clean.LedgerEntries != 2 || clean.Records != 2 {
		t.Fatalf("counts = %d ledger entries, %d audit records", clean.LedgerEntries, clean.Records)
	}

	ledger, err := st.LoadLedger()
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if len(ledger) != 2 {
		t.Fatalf("ledger entries = %d", len(ledger))
	}
	path := st.Path(LedgerFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	const replacement = "1111111111111111111111111111111111111111111111111111111111111111"
	tampered := strings.Replace(string(data), ledger[0].PayloadHash, replacement, 1)
	if tampered == string(data) {
		t.Fatal("test setup failed to modify the ledger")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write ledger: %v", err)
	}

	chain, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if chain.Valid {
		t.Fatalf("expected the store verdict to reject the altered ledger: %+v", chain)
	}
	if len(chain.Problems) == 0 {
		t.Fatalf("expected at least one problem, notes were %+v", chain.Notes)
	}
	kinds := map[string]bool{}
	for _, p := range chain.Problems {
		kinds[p.Kind] = true
	}
	if !kinds[ProblemHashMismatch] {
		t.Fatalf("problems = %+v, want a payload hash mismatch", chain.Problems)
	}
	reloaded, err := st.LoadLedger()
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	if reloaded[0].PayloadHash != replacement {
		t.Fatalf("ledger payload hash = %q, the tampering did not survive", reloaded[0].PayloadHash)
	}
}
