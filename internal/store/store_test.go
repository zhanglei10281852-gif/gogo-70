package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"CableMend/internal/model"
)

func evidence(id string, at string) model.Evidence {
	dist := 12.5
	return model.Evidence{
		ID: id, SystemID: "SYS-T", FaultID: "F-1", ObservedAt: model.MustParseUTC(at),
		End: model.EndA, Method: model.MethodOTDR, CableDistanceKm: &dist,
	}
}

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st
}

func TestAppendEvidenceIsIdempotent(t *testing.T) {
	st := open(t)
	first, err := st.AppendEvidence([]model.Evidence{
		evidence("EV-2", "2026-03-10T02:00:00Z"),
		evidence("EV-1", "2026-03-10T01:00:00Z"),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if first.Appended != 2 || first.Duplicate != 0 || first.Total != 2 {
		t.Fatalf("first append = %+v", first)
	}
	second, err := st.AppendEvidence([]model.Evidence{evidence("EV-1", "2026-03-10T01:00:00Z")})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if second.Appended != 0 || second.Duplicate != 1 {
		t.Fatalf("second append = %+v", second)
	}
	records, err := st.LoadEvidence()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 2 || records[0].ID != "EV-1" {
		t.Fatalf("records = %d, first %q", len(records), records[0].ID)
	}
}

func TestCommitWritesLedgerAuditAndSnapshot(t *testing.T) {
	st := open(t)
	if _, err := st.AppendEvidence([]model.Evidence{evidence("EV-1", "2026-03-10T01:00:00Z")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	entry, audit, err := st.Commit(Event{
		Kind:     "ingest",
		At:       model.MustParseUTC("2026-03-10T01:00:00Z"),
		Subject:  "SYS-T",
		Summary:  "one record",
		Snapshot: "ingest-SYS-T",
		Payload:  map[string]int{"records": 1},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if entry.Seq != 1 || audit.Seq != 1 {
		t.Fatalf("sequences = %d, %d", entry.Seq, audit.Seq)
	}
	if audit.PrevHash != GenesisHash {
		t.Fatalf("first record prev hash = %q", audit.PrevHash)
	}
	if entry.PayloadHash != audit.PayloadHash {
		t.Fatal("ledger and audit payload hashes must match")
	}
	if entry.SnapshotPath == "" {
		t.Fatal("expected a snapshot path")
	}
	var snapshot map[string]int
	if err := st.ReadSnapshot(entry.SnapshotPath, &snapshot); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snapshot["records"] != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	meta, err := st.ReadMeta()
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.EvidenceCount != 1 || meta.LedgerCount != 1 || meta.AuditCount != 1 || meta.SnapshotCount != 1 {
		t.Fatalf("meta = %+v", meta)
	}
	if meta.LastAuditHash != audit.Hash {
		t.Fatalf("meta head = %q, want %q", meta.LastAuditHash, audit.Hash)
	}
	if len(meta.Systems) != 1 || meta.Systems[0] != "SYS-T" {
		t.Fatalf("meta systems = %v", meta.Systems)
	}
}

func TestCommitRejectsMissingTimestamp(t *testing.T) {
	st := open(t)
	if _, _, err := st.Commit(Event{Kind: "ingest", Payload: map[string]int{}}); err == nil {
		t.Fatal("expected an error without an input-derived timestamp")
	}
	if _, _, err := st.Commit(Event{At: model.MustParseUTC("2026-03-10T00:00:00Z")}); err == nil {
		t.Fatal("expected an error without a kind")
	}
}

func TestAuditChainLinksAndVerifies(t *testing.T) {
	st := open(t)
	for i, at := range []string{"2026-03-10T01:00:00Z", "2026-03-11T01:00:00Z", "2026-03-12T01:00:00Z"} {
		if _, _, err := st.Commit(Event{
			Kind: "plan", At: model.MustParseUTC(at), Subject: "SYS-T",
			Summary: "entry", Payload: map[string]int{"index": i},
		}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	records, err := st.LoadAudit()
	if err != nil {
		t.Fatalf("load audit: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("audit records = %d", len(records))
	}
	for i := 1; i < len(records); i++ {
		if records[i].PrevHash != records[i-1].Hash {
			t.Fatalf("record %d is not linked to its predecessor", i+1)
		}
	}
	chain, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !chain.Valid || len(chain.Problems) != 0 {
		t.Fatalf("chain = %+v", chain)
	}
	if chain.HeadHash != records[2].Hash {
		t.Fatalf("head = %q", chain.HeadHash)
	}
}

func TestVerifyAuditDetectsTampering(t *testing.T) {
	st := open(t)
	for _, at := range []string{"2026-03-10T01:00:00Z", "2026-03-11T01:00:00Z"} {
		if _, _, err := st.Commit(Event{
			Kind: "plan", At: model.MustParseUTC(at), Subject: "SYS-T", Summary: "entry",
			Payload: map[string]string{"at": at},
		}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	path := st.Path(AuditFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tampered := strings.Replace(string(data), `"subject":"SYS-T"`, `"subject":"SYS-X"`, 1)
	if tampered == string(data) {
		t.Fatal("test setup failed to modify the chain")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	chain, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if chain.Valid {
		t.Fatal("expected the tampered chain to fail verification")
	}
	kinds := map[string]bool{}
	for _, p := range chain.Problems {
		kinds[p.Kind] = true
	}
	if !kinds[ProblemHashMismatch] {
		t.Fatalf("problems = %+v, want a hash mismatch", chain.Problems)
	}
}

func TestSnapshotWriteIsAtomicAndSanitized(t *testing.T) {
	st := open(t)
	rel, hash, err := st.WriteSnapshot("plan/SYS-T:F-1", map[string]string{"vessel": "CS-ONE"})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if strings.ContainsAny(filepath.Base(rel), ":/") {
		t.Fatalf("snapshot name was not sanitized: %q", rel)
	}
	if hash == "" {
		t.Fatal("expected a content hash")
	}
	entries, err := os.ReadDir(filepath.Join(st.Root(), SnapshotDir))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
	again, hashAgain, err := st.WriteSnapshot("plan/SYS-T:F-1", map[string]string{"vessel": "CS-ONE"})
	if err != nil {
		t.Fatalf("snapshot rewrite: %v", err)
	}
	if again != rel || hashAgain != hash {
		t.Fatal("rewriting the same document must be stable")
	}
}

func TestLoadRejectsCorruptRecords(t *testing.T) {
	st := open(t)
	if err := os.WriteFile(st.Path(EvidenceFile), []byte("{\"id\":\"EV-1\",\"nope\":1}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := st.LoadEvidence(); err == nil {
		t.Fatal("expected strict decoding to reject an unknown field")
	}
}

func TestEmptyStoreReadsCleanly(t *testing.T) {
	st := open(t)
	records, err := st.LoadEvidence()
	if err != nil || len(records) != 0 {
		t.Fatalf("evidence = %v, %v", records, err)
	}
	ledger, err := st.LoadLedger()
	if err != nil || len(ledger) != 0 {
		t.Fatalf("ledger = %v, %v", ledger, err)
	}
	chain, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !chain.Valid {
		t.Fatal("an empty chain is valid")
	}
	meta, err := st.ReadMeta()
	if err != nil || meta.Version != MetaVersion {
		t.Fatalf("meta = %+v, %v", meta, err)
	}
}

func TestOpenRejectsEmptyRoot(t *testing.T) {
	if _, err := Open("  "); err == nil {
		t.Fatal("expected an error for an empty store directory")
	}
}

func TestVerifyAuditTreatsEarlierEventTimesAsANote(t *testing.T) {
	st := open(t)
	// Event times come from the input data, so a later commit may describe an
	// earlier instant. That must not invalidate the chain.
	for _, at := range []string{"2026-03-12T00:00:00Z", "2026-03-10T00:00:00Z"} {
		if _, _, err := st.Commit(Event{
			Kind: "plan", At: model.MustParseUTC(at), Subject: "SYS-T", Summary: "entry",
			Payload: map[string]string{"at": at},
		}); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	chain, err := st.VerifyAudit()
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !chain.Valid {
		t.Fatalf("chain must stay valid: %+v", chain.Problems)
	}
	if len(chain.Notes) != 1 || chain.Notes[0].Kind != ProblemTimeReversed {
		t.Fatalf("notes = %+v", chain.Notes)
	}
}
