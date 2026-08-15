package model

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/numeric"
)

// FaultsSchemaVersion is the accepted version of a faults document.
const FaultsSchemaVersion = 1

// FaultsDocument is the campaign input file listing the faults to plan for.
type FaultsDocument struct {
	Version int           `json:"version"`
	Faults  []FaultRecord `json:"faults"`
}

// FaultRecord is one declared fault awaiting repair.
type FaultRecord struct {
	ID               string  `json:"id"`
	SystemID         string  `json:"system_id"`
	ReportedAt       UTCTime `json:"reported_at"`
	DeclaredPriority float64 `json:"declared_priority"`
	TrafficTbps      float64 `json:"traffic_tbps"`
	SingleRoute      bool    `json:"single_route"`
	RestoreBy        UTCTime `json:"restore_by"`
	Description      string  `json:"description"`
}

// Validate checks the faults document.
func (d FaultsDocument) Validate() []string {
	var problems []string
	if d.Version != FaultsSchemaVersion {
		problems = append(problems, fmt.Sprintf("faults: version must be %d, got %d", FaultsSchemaVersion, d.Version))
	}
	if len(d.Faults) == 0 {
		problems = append(problems, "faults: at least one fault is required")
	}
	seen := make(map[string]bool, len(d.Faults))
	for _, f := range d.Faults {
		if strings.TrimSpace(f.ID) == "" {
			problems = append(problems, "faults: fault id must not be empty")
			continue
		}
		if seen[f.ID] {
			problems = append(problems, fmt.Sprintf("faults: duplicate fault id %q", f.ID))
		}
		seen[f.ID] = true
		problems = append(problems, f.Validate()...)
	}
	sort.Strings(problems)
	return problems
}

// Validate checks one fault record.
func (f FaultRecord) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("fault %s: %s", f.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(f.SystemID) == "" {
		add("system_id must not be empty")
	}
	if f.ReportedAt.IsZero() {
		add("reported_at must be set")
	}
	if !numeric.NonNegative(f.DeclaredPriority) || f.DeclaredPriority > 100 {
		add("declared_priority must be within [0, 100]")
	}
	if !numeric.NonNegative(f.TrafficTbps) {
		add("traffic_tbps must not be negative")
	}
	if !f.RestoreBy.IsZero() && !f.ReportedAt.IsZero() && f.RestoreBy.Before(f.ReportedAt) {
		add("restore_by must not precede reported_at")
	}
	return problems
}

// SortFaults orders faults by id.
func SortFaults(faults []FaultRecord) []FaultRecord {
	out := make([]FaultRecord, len(faults))
	copy(out, faults)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VerificationRecord is a post-repair measurement set for one segment.
type VerificationRecord struct {
	ID                string  `json:"id"`
	SystemID          string  `json:"system_id"`
	SegmentID         string  `json:"segment_id"`
	FaultID           string  `json:"fault_id"`
	MeasuredAt        UTCTime `json:"measured_at"`
	MeasuredLossDb    float64 `json:"measured_loss_db"`
	JointsAfterRepair int     `json:"joints_after_repair"`
	RepairKP          float64 `json:"repair_kp"`
	SpareCableUsedKm  float64 `json:"spare_cable_used_km"`
	BurialAchievedM   float64 `json:"burial_achieved_m"`
	ROVInspection     bool    `json:"rov_inspection"`
	Notes             string  `json:"notes"`
}

// Validate checks one verification record.
func (v VerificationRecord) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("verification %s: %s", v.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(v.ID) == "" {
		problems = append(problems, "verification: id must not be empty")
	}
	if strings.TrimSpace(v.SystemID) == "" {
		add("system_id must not be empty")
	}
	if strings.TrimSpace(v.SegmentID) == "" {
		add("segment_id must not be empty")
	}
	if strings.TrimSpace(v.FaultID) == "" {
		add("fault_id must not be empty")
	}
	if v.MeasuredAt.IsZero() {
		add("measured_at must be set")
	}
	if !numeric.NonNegative(v.MeasuredLossDb) {
		add("measured_loss_db must not be negative")
	}
	if v.JointsAfterRepair < 0 {
		add("joints_after_repair must not be negative")
	}
	if !numeric.NonNegative(v.RepairKP) {
		add("repair_kp must not be negative")
	}
	if !numeric.NonNegative(v.SpareCableUsedKm) {
		add("spare_cable_used_km must not be negative")
	}
	if !numeric.NonNegative(v.BurialAchievedM) {
		add("burial_achieved_m must not be negative")
	}
	return problems
}

// SortVerifications orders verification records by measurement time then id.
func SortVerifications(records []VerificationRecord) []VerificationRecord {
	out := make([]VerificationRecord, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].MeasuredAt.Equal(out[j].MeasuredAt) {
			return out[i].MeasuredAt.Before(out[j].MeasuredAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
