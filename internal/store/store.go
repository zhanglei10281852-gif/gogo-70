// Package store is the local persistent state: an append-only evidence log, an
// append-only work ledger, plan snapshots, a metadata document and a hash-chained
// audit log. Every write is atomic (temporary file plus rename) and every stored
// timestamp comes from the input data, never from the wall clock.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"CableMend/internal/jsonio"
	"CableMend/internal/model"
)

// File and directory names inside the store.
const (
	EvidenceFile  = "evidence.jsonl"
	LedgerFile    = "ledger.jsonl"
	AuditFile     = "audit.jsonl"
	MetaFile      = "meta.json"
	SnapshotDir   = "snapshots"
	MetaVersion   = 1
	tempSuffix    = ".tmp"
	dirPermission = 0o755
)

// Store is an opened local store rooted at a directory.
type Store struct {
	root string
}

// Meta is the store metadata document.
type Meta struct {
	Version       int           `json:"version"`
	EvidenceCount int           `json:"evidence_count"`
	LedgerCount   int           `json:"ledger_count"`
	AuditCount    int           `json:"audit_count"`
	SnapshotCount int           `json:"snapshot_count"`
	Systems       []string      `json:"systems"`
	Faults        []string      `json:"faults"`
	LastEventAt   model.UTCTime `json:"last_event_at"`
	LastEventKind string        `json:"last_event_kind,omitempty"`
	LastAuditHash string        `json:"last_audit_hash,omitempty"`
}

// LedgerEntry is one work-ledger record.
type LedgerEntry struct {
	Seq          int           `json:"seq"`
	Kind         string        `json:"kind"`
	At           model.UTCTime `json:"at"`
	Subject      string        `json:"subject"`
	Summary      string        `json:"summary"`
	SnapshotPath string        `json:"snapshot_path,omitempty"`
	PayloadHash  string        `json:"payload_sha256"`
}

// Open prepares a store directory, creating it when needed.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("store directory must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve store directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, SnapshotDir), dirPermission); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	return &Store{root: abs}, nil
}

// Root returns the absolute store directory.
func (s *Store) Root() string { return s.root }

// Path joins a name onto the store root.
func (s *Store) Path(name string) string { return filepath.Join(s.root, name) }

// AppendResult reports what an evidence append did.
type AppendResult struct {
	Appended  int      `json:"appended"`
	Duplicate int      `json:"duplicate"`
	Total     int      `json:"total"`
	NewIDs    []string `json:"new_ids"`
}

// AppendEvidence appends records that are not already present, keyed by record
// id. The operation is idempotent, so re-ingesting a file is safe.
func (s *Store) AppendEvidence(records []model.Evidence) (AppendResult, error) {
	existing, err := s.LoadEvidence()
	if err != nil {
		return AppendResult{}, err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[e.ID] = true
	}
	var buf strings.Builder
	result := AppendResult{Total: len(existing)}
	for _, rec := range model.SortEvidence(records) {
		if seen[rec.ID] {
			result.Duplicate++
			continue
		}
		seen[rec.ID] = true
		line, err := jsonio.MarshalLine(rec)
		if err != nil {
			return AppendResult{}, fmt.Errorf("encode evidence %s: %w", rec.ID, err)
		}
		buf.Write(line)
		result.Appended++
		result.NewIDs = append(result.NewIDs, rec.ID)
	}
	if result.Appended > 0 {
		if err := appendFile(s.Path(EvidenceFile), []byte(buf.String())); err != nil {
			return AppendResult{}, err
		}
	}
	result.Total += result.Appended
	sort.Strings(result.NewIDs)
	return result, nil
}

// LoadEvidence reads the evidence log.
func (s *Store) LoadEvidence() ([]model.Evidence, error) {
	path := s.Path(EvidenceFile)
	if !fileExists(path) {
		return nil, nil
	}
	var out []model.Evidence
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var rec model.Evidence
		if err := jsonio.DecodeRecord(raw, &rec); err != nil {
			return err
		}
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.SortEvidence(out), nil
}

// LoadLedger reads the work ledger.
func (s *Store) LoadLedger() ([]LedgerEntry, error) {
	path := s.Path(LedgerFile)
	if !fileExists(path) {
		return nil, nil
	}
	var out []LedgerEntry
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var entry LedgerEntry
		if err := jsonio.DecodeRecord(raw, &entry); err != nil {
			return err
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// WriteSnapshot stores a document under snapshots/ and returns its relative path
// and content hash.
func (s *Store) WriteSnapshot(name string, doc any) (string, string, error) {
	if strings.TrimSpace(name) == "" {
		return "", "", fmt.Errorf("snapshot name must not be empty")
	}
	safe := sanitize(name)
	data, err := jsonio.Marshal(doc)
	if err != nil {
		return "", "", fmt.Errorf("encode snapshot %s: %w", safe, err)
	}
	rel := filepath.ToSlash(filepath.Join(SnapshotDir, safe+".json"))
	if err := writeAtomic(s.Path(rel), data); err != nil {
		return "", "", err
	}
	return rel, HashBytes(data), nil
}

// ReadSnapshot decodes a snapshot document.
func (s *Store) ReadSnapshot(relPath string, doc any) error {
	return jsonio.DecodeFile(s.Path(relPath), doc)
}

// Event describes something worth recording in the ledger and audit chain.
type Event struct {
	Kind     string
	At       model.UTCTime
	Subject  string
	Summary  string
	Snapshot string
	Payload  any
}

// Commit writes an event: optional snapshot, ledger entry, audit record and
// refreshed metadata. The returned entry carries the assigned sequence number.
func (s *Store) Commit(ev Event) (LedgerEntry, AuditRecord, error) {
	if strings.TrimSpace(ev.Kind) == "" {
		return LedgerEntry{}, AuditRecord{}, fmt.Errorf("event kind must not be empty")
	}
	if ev.At.IsZero() {
		return LedgerEntry{}, AuditRecord{}, fmt.Errorf("event %s has no input-derived timestamp", ev.Kind)
	}
	payload, err := jsonio.Marshal(ev.Payload)
	if err != nil {
		return LedgerEntry{}, AuditRecord{}, fmt.Errorf("encode payload: %w", err)
	}
	snapshotPath := ""
	if ev.Snapshot != "" {
		path, _, err := s.WriteSnapshot(ev.Snapshot, ev.Payload)
		if err != nil {
			return LedgerEntry{}, AuditRecord{}, err
		}
		snapshotPath = path
	}
	ledger, err := s.LoadLedger()
	if err != nil {
		return LedgerEntry{}, AuditRecord{}, err
	}
	entry := LedgerEntry{
		Seq:          len(ledger) + 1,
		Kind:         ev.Kind,
		At:           ev.At,
		Subject:      ev.Subject,
		Summary:      ev.Summary,
		SnapshotPath: snapshotPath,
		PayloadHash:  HashBytes(payload),
	}
	line, err := jsonio.MarshalLine(entry)
	if err != nil {
		return LedgerEntry{}, AuditRecord{}, fmt.Errorf("encode ledger entry: %w", err)
	}
	if err := appendFile(s.Path(LedgerFile), line); err != nil {
		return LedgerEntry{}, AuditRecord{}, err
	}
	audit, err := s.AppendAudit(entry)
	if err != nil {
		return LedgerEntry{}, AuditRecord{}, err
	}
	if err := s.RefreshMeta(); err != nil {
		return LedgerEntry{}, AuditRecord{}, err
	}
	return entry, audit, nil
}

// RefreshMeta recomputes and stores the metadata document.
func (s *Store) RefreshMeta() error {
	evidence, err := s.LoadEvidence()
	if err != nil {
		return err
	}
	ledger, err := s.LoadLedger()
	if err != nil {
		return err
	}
	audit, err := s.LoadAudit()
	if err != nil {
		return err
	}
	meta := Meta{
		Version:       MetaVersion,
		EvidenceCount: len(evidence),
		LedgerCount:   len(ledger),
		AuditCount:    len(audit),
	}
	systems := map[string]bool{}
	faults := map[string]bool{}
	for _, e := range evidence {
		systems[e.SystemID] = true
		faults[e.FaultID] = true
	}
	meta.Systems = sortedKeys(systems)
	meta.Faults = sortedKeys(faults)
	if len(ledger) > 0 {
		last := ledger[len(ledger)-1]
		meta.LastEventAt = last.At
		meta.LastEventKind = last.Kind
	}
	if len(audit) > 0 {
		meta.LastAuditHash = audit[len(audit)-1].Hash
	}
	snapshots, err := os.ReadDir(filepath.Join(s.root, SnapshotDir))
	if err == nil {
		for _, entry := range snapshots {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				meta.SnapshotCount++
			}
		}
	}
	data, err := jsonio.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	return writeAtomic(s.Path(MetaFile), data)
}

// ReadMeta loads the metadata document, returning an empty document when the
// store has never been written.
func (s *Store) ReadMeta() (Meta, error) {
	path := s.Path(MetaFile)
	if !fileExists(path) {
		return Meta{Version: MetaVersion}, nil
	}
	var meta Meta
	if err := jsonio.DecodeFile(path, &meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

// HashBytes returns the hex-encoded SHA-256 of the payload.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeAtomic writes data through a temporary file in the same directory and then
// renames it over the destination.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPermission); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp := path + tempSuffix
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if fileExists(path) {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("replace %s: %w", path, err)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmp, err)
	}
	return nil
}

// appendFile appends bytes to an append-only log.
func appendFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPermission); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("append %s: %w", path, err)
	}
	return f.Close()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sanitize keeps snapshot names to a safe, portable character set.
func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "snapshot"
	}
	return out
}
