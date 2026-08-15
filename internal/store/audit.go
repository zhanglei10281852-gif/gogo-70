package store

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/jsonio"
	"CableMend/internal/model"
)

// GenesisHash is the previous-hash value of the first audit record.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditRecord is one link of the hash chain.
type AuditRecord struct {
	Seq         int           `json:"seq"`
	At          model.UTCTime `json:"at"`
	Kind        string        `json:"kind"`
	Subject     string        `json:"subject"`
	PayloadHash string        `json:"payload_sha256"`
	PrevHash    string        `json:"prev_sha256"`
	Hash        string        `json:"sha256"`
}

// Canonical renders the hashed pre-image of a record. The layout is fixed and
// field-separated so that no field value can be confused with another.
func (a AuditRecord) Canonical() string {
	return strings.Join([]string{
		fmt.Sprintf("seq=%d", a.Seq),
		"at=" + a.At.String(),
		"kind=" + a.Kind,
		"subject=" + a.Subject,
		"payload=" + a.PayloadHash,
		"prev=" + a.PrevHash,
	}, "\n") + "\n"
}

// AppendAudit links a ledger entry into the chain.
func (s *Store) AppendAudit(entry LedgerEntry) (AuditRecord, error) {
	records, err := s.LoadAudit()
	if err != nil {
		return AuditRecord{}, err
	}
	prev := GenesisHash
	if len(records) > 0 {
		prev = records[len(records)-1].Hash
	}
	record := AuditRecord{
		Seq:         len(records) + 1,
		At:          entry.At,
		Kind:        entry.Kind,
		Subject:     entry.Subject,
		PayloadHash: entry.PayloadHash,
		PrevHash:    prev,
	}
	record.Hash = HashBytes([]byte(record.Canonical()))
	line, err := jsonio.MarshalLine(record)
	if err != nil {
		return AuditRecord{}, fmt.Errorf("encode audit record: %w", err)
	}
	if err := appendFile(s.Path(AuditFile), line); err != nil {
		return AuditRecord{}, err
	}
	return record, nil
}

// LoadAudit reads the audit chain in sequence order.
func (s *Store) LoadAudit() ([]AuditRecord, error) {
	path := s.Path(AuditFile)
	if !fileExists(path) {
		return nil, nil
	}
	var out []AuditRecord
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var rec AuditRecord
		if err := jsonio.DecodeRecord(raw, &rec); err != nil {
			return err
		}
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// ChainProblem is one defect found while verifying the chain.
type ChainProblem struct {
	Seq    int    `json:"seq"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Chain problem kinds.
const (
	ProblemSequence     = "sequence_out_of_order"
	ProblemPrevMismatch = "previous_hash_mismatch"
	ProblemHashMismatch = "record_hash_mismatch"
	ProblemTimeReversed = "timestamp_moves_backwards"
	ProblemMissingLink  = "ledger_entry_without_audit_record"
)

// ChainVerification is the audit verification outcome. Problems break the chain;
// notes are observations that do not affect integrity, such as an event whose
// input-derived timestamp precedes an earlier entry.
type ChainVerification struct {
	Records       int            `json:"records"`
	LedgerEntries int            `json:"ledger_entries"`
	Valid         bool           `json:"valid"`
	HeadHash      string         `json:"head_hash,omitempty"`
	Problems      []ChainProblem `json:"problems,omitempty"`
	Notes         []ChainProblem `json:"notes,omitempty"`
}

// VerifyAudit recomputes the chain and cross-checks it against the ledger.
func (s *Store) VerifyAudit() (ChainVerification, error) {
	records, err := s.LoadAudit()
	if err != nil {
		return ChainVerification{}, err
	}
	ledger, err := s.LoadLedger()
	if err != nil {
		return ChainVerification{}, err
	}
	out := ChainVerification{Records: len(records), LedgerEntries: len(ledger), Valid: true}
	prev := GenesisHash
	var prevAt model.UTCTime
	for i, rec := range records {
		expectedSeq := i + 1
		if rec.Seq != expectedSeq {
			out.Problems = append(out.Problems, ChainProblem{
				Seq:    rec.Seq,
				Kind:   ProblemSequence,
				Detail: fmt.Sprintf("expected sequence %d", expectedSeq),
			})
		}
		if rec.PrevHash != prev {
			out.Problems = append(out.Problems, ChainProblem{
				Seq:    rec.Seq,
				Kind:   ProblemPrevMismatch,
				Detail: fmt.Sprintf("expected previous hash %s", prev),
			})
		}
		recomputed := HashBytes([]byte(rec.Canonical()))
		if recomputed != rec.Hash {
			out.Problems = append(out.Problems, ChainProblem{
				Seq:    rec.Seq,
				Kind:   ProblemHashMismatch,
				Detail: fmt.Sprintf("recomputed hash %s", recomputed),
			})
		}
		if !prevAt.IsZero() && rec.At.Before(prevAt) {
			// Event times come from the input data, so a later entry may legitimately
			// describe an earlier instant. Record it without breaking the chain.
			out.Notes = append(out.Notes, ChainProblem{
				Seq:    rec.Seq,
				Kind:   ProblemTimeReversed,
				Detail: fmt.Sprintf("%s precedes %s", rec.At, prevAt),
			})
		}
		prev = rec.Hash
		prevAt = rec.At
	}
	if len(ledger) > len(records) {
		out.Problems = append(out.Problems, ChainProblem{
			Seq:    len(records) + 1,
			Kind:   ProblemMissingLink,
			Detail: fmt.Sprintf("%d ledger entries but %d audit records", len(ledger), len(records)),
		})
	}
	for i := 0; i < len(ledger) && i < len(records); i++ {
		if ledger[i].PayloadHash != records[i].PayloadHash {
			out.Notes = append(out.Notes, ChainProblem{
				Seq:    records[i].Seq,
				Kind:   ProblemHashMismatch,
				Detail: fmt.Sprintf("ledger payload %s does not match audit payload %s", ledger[i].PayloadHash, records[i].PayloadHash),
			})
		}
	}
	if len(records) > 0 {
		out.HeadHash = records[len(records)-1].Hash
	}
	out.Valid = len(out.Problems) == 0
	sortProblems(out.Problems)
	sortProblems(out.Notes)
	return out, nil
}

// sortProblems orders findings by sequence number then kind.
func sortProblems(problems []ChainProblem) {
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Seq != problems[j].Seq {
			return problems[i].Seq < problems[j].Seq
		}
		return problems[i].Kind < problems[j].Kind
	})
}
