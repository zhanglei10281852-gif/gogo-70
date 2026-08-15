package model

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/numeric"
)

// Method is a fault measurement technique.
type Method string

// Supported measurement methods.
const (
	MethodOTDR       Method = "otdr"
	MethodResistance Method = "resistance"
	MethodVoltage    Method = "voltage"
)

// Valid reports whether the method is supported.
func (m Method) Valid() bool {
	switch m {
	case MethodOTDR, MethodResistance, MethodVoltage:
		return true
	default:
		return false
	}
}

// FaultClass is the electrical nature of the fault.
type FaultClass string

// Supported fault classes.
const (
	ClassShunt   FaultClass = "shunt"
	ClassOpen    FaultClass = "open"
	ClassUnknown FaultClass = "unknown"
)

// Valid reports whether the class is supported.
func (f FaultClass) Valid() bool {
	switch f {
	case ClassShunt, ClassOpen, ClassUnknown:
		return true
	default:
		return false
	}
}

// Evidence is one measurement taken from one end of one cable system.
type Evidence struct {
	ID                    string     `json:"id"`
	SystemID              string     `json:"system_id"`
	FaultID               string     `json:"fault_id"`
	ObservedAt            UTCTime    `json:"observed_at"`
	End                   End        `json:"end"`
	Method                Method     `json:"method"`
	Instrument            string     `json:"instrument"`
	CableDistanceKm       *float64   `json:"cable_distance_km"`
	ResistanceOhm         *float64   `json:"resistance_ohm"`
	ConductorOhmPerKm     *float64   `json:"conductor_ohm_per_km"`
	VoltageV              *float64   `json:"voltage_v"`
	VoltageGradientVPerKm *float64   `json:"voltage_gradient_v_per_km"`
	LossStepDb            *float64   `json:"loss_step_db"`
	InsulationMohm        *float64   `json:"insulation_resistance_mohm"`
	UncertaintyKm         *float64   `json:"uncertainty_km"`
	DeclaredClass         FaultClass `json:"fault_class"`
	Notes                 string     `json:"notes"`
}

// Validate checks the record in isolation. Cross-checks against the cable
// system happen during ingest and localization.
func (e Evidence) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("evidence %s: %s", e.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(e.ID) == "" {
		problems = append(problems, "evidence: id must not be empty")
	}
	if strings.TrimSpace(e.SystemID) == "" {
		add("system_id must not be empty")
	}
	if strings.TrimSpace(e.FaultID) == "" {
		add("fault_id must not be empty")
	}
	if e.ObservedAt.IsZero() {
		add("observed_at must be set")
	}
	if !e.End.Valid() {
		add("end must be A or B, got %q", string(e.End))
	}
	if !e.Method.Valid() {
		add("method must be otdr, resistance or voltage, got %q", string(e.Method))
	}
	if e.DeclaredClass != "" && !e.DeclaredClass.Valid() {
		add("fault_class must be shunt, open or unknown, got %q", string(e.DeclaredClass))
	}
	switch e.Method {
	case MethodOTDR:
		if e.CableDistanceKm == nil {
			add("method otdr requires cable_distance_km")
		} else if !numeric.NonNegative(*e.CableDistanceKm) {
			add("cable_distance_km must not be negative")
		}
	case MethodResistance:
		if e.ResistanceOhm == nil || e.ConductorOhmPerKm == nil {
			add("method resistance requires resistance_ohm and conductor_ohm_per_km")
		} else {
			if !numeric.NonNegative(*e.ResistanceOhm) {
				add("resistance_ohm must not be negative")
			}
			if !numeric.Positive(*e.ConductorOhmPerKm) {
				add("conductor_ohm_per_km must be positive")
			}
		}
	case MethodVoltage:
		if e.VoltageV == nil || e.VoltageGradientVPerKm == nil {
			add("method voltage requires voltage_v and voltage_gradient_v_per_km")
		} else {
			if !numeric.NonNegative(*e.VoltageV) {
				add("voltage_v must not be negative")
			}
			if !numeric.Positive(*e.VoltageGradientVPerKm) {
				add("voltage_gradient_v_per_km must be positive")
			}
		}
	}
	if e.UncertaintyKm != nil && !numeric.Positive(*e.UncertaintyKm) {
		add("uncertainty_km must be positive when provided")
	}
	if e.LossStepDb != nil && !numeric.NonNegative(*e.LossStepDb) {
		add("loss_step_db must not be negative")
	}
	if e.InsulationMohm != nil && !numeric.NonNegative(*e.InsulationMohm) {
		add("insulation_resistance_mohm must not be negative")
	}
	return problems
}

// MeasuredCableKm derives the cable distance implied by the record.
func (e Evidence) MeasuredCableKm() (float64, error) {
	switch e.Method {
	case MethodOTDR:
		if e.CableDistanceKm == nil {
			return 0, fmt.Errorf("evidence %s: missing cable_distance_km", e.ID)
		}
		return *e.CableDistanceKm, nil
	case MethodResistance:
		if e.ResistanceOhm == nil || e.ConductorOhmPerKm == nil {
			return 0, fmt.Errorf("evidence %s: missing resistance inputs", e.ID)
		}
		if *e.ConductorOhmPerKm <= 0 {
			return 0, fmt.Errorf("evidence %s: conductor_ohm_per_km must be positive", e.ID)
		}
		return *e.ResistanceOhm / *e.ConductorOhmPerKm, nil
	case MethodVoltage:
		if e.VoltageV == nil || e.VoltageGradientVPerKm == nil {
			return 0, fmt.Errorf("evidence %s: missing voltage inputs", e.ID)
		}
		if *e.VoltageGradientVPerKm <= 0 {
			return 0, fmt.Errorf("evidence %s: voltage_gradient_v_per_km must be positive", e.ID)
		}
		return *e.VoltageV / *e.VoltageGradientVPerKm, nil
	default:
		return 0, fmt.Errorf("evidence %s: unsupported method %q", e.ID, string(e.Method))
	}
}

// Classify resolves the fault class of the record. A declared class wins; other
// wise the insulation reading decides, with the loss step as a weak fallback.
func (e Evidence) Classify(shuntThresholdMohm float64) FaultClass {
	if e.DeclaredClass.Valid() && e.DeclaredClass != ClassUnknown {
		return e.DeclaredClass
	}
	if e.InsulationMohm != nil {
		if *e.InsulationMohm < shuntThresholdMohm {
			return ClassShunt
		}
		return ClassOpen
	}
	if e.LossStepDb != nil && *e.LossStepDb > 0 {
		return ClassShunt
	}
	return ClassUnknown
}

// LossStep returns the reported loss step, or zero when absent.
func (e Evidence) LossStep() float64 {
	if e.LossStepDb == nil {
		return 0
	}
	return *e.LossStepDb
}

// SortEvidence orders records by observation time, then id, giving a stable
// ledger order independent of file order.
func SortEvidence(records []Evidence) []Evidence {
	out := make([]Evidence, len(records))
	copy(out, records)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].ObservedAt.Equal(out[j].ObservedAt) {
			return out[i].ObservedAt.Before(out[j].ObservedAt)
		}
		if out[i].SystemID != out[j].SystemID {
			return out[i].SystemID < out[j].SystemID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// FilterEvidence keeps records matching a system and, when non-empty, a fault.
func FilterEvidence(records []Evidence, systemID, faultID string) []Evidence {
	out := make([]Evidence, 0, len(records))
	for _, e := range records {
		if systemID != "" && e.SystemID != systemID {
			continue
		}
		if faultID != "" && e.FaultID != faultID {
			continue
		}
		out = append(out, e)
	}
	return out
}

// GroupEvidenceByFault buckets records by fault id and returns the sorted keys
// alongside the map.
func GroupEvidenceByFault(records []Evidence) ([]string, map[string][]Evidence) {
	groups := make(map[string][]Evidence)
	for _, e := range records {
		groups[e.FaultID] = append(groups[e.FaultID], e)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
		groups[k] = SortEvidence(groups[k])
	}
	sort.Strings(keys)
	return keys, groups
}

// GroupEvidenceBySystem buckets records by system id.
func GroupEvidenceBySystem(records []Evidence) ([]string, map[string][]Evidence) {
	groups := make(map[string][]Evidence)
	for _, e := range records {
		groups[e.SystemID] = append(groups[e.SystemID], e)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
		groups[k] = SortEvidence(groups[k])
	}
	sort.Strings(keys)
	return keys, groups
}

// EvidenceIDs returns the record identifiers in slice order.
func EvidenceIDs(records []Evidence) []string {
	out := make([]string, 0, len(records))
	for _, e := range records {
		out = append(out, e.ID)
	}
	return out
}
