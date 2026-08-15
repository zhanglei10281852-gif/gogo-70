// Package permits evaluates whether at-sea work is authorized. Every protection
// zone overlapping the repair window that is marked permit_required must be
// covered by a single permit whose validity interval contains the whole work
// window and whose operation list contains every planned operation.
package permits

import (
	"fmt"
	"sort"

	"CableMend/internal/model"
)

// PermitStatus is the evaluation of one candidate permit against a window.
type PermitStatus struct {
	PermitID          string        `json:"permit_id"`
	Reference         string        `json:"reference,omitempty"`
	ValidFrom         model.UTCTime `json:"valid_from"`
	ValidTo           model.UTCTime `json:"valid_to"`
	Operations        []string      `json:"operations"`
	CoversWindow      bool          `json:"covers_window"`
	CoversOperations  bool          `json:"covers_operations"`
	MissingOperations []string      `json:"missing_operations,omitempty"`
}

// Usable reports whether the permit authorizes the whole window.
func (p PermitStatus) Usable() bool { return p.CoversWindow && p.CoversOperations }

// ZoneRequirement is the permit position for one protection zone.
type ZoneRequirement struct {
	ZoneID           string         `json:"zone_id"`
	Jurisdiction     string         `json:"jurisdiction"`
	PermitRequired   bool           `json:"permit_required"`
	Satisfied        bool           `json:"satisfied"`
	CoveringPermitID string         `json:"covering_permit_id,omitempty"`
	Candidates       []PermitStatus `json:"candidates"`
	Note             string         `json:"note,omitempty"`
}

// Assessment is the full permit picture for one repair window.
type Assessment struct {
	SystemID      string            `json:"system_id"`
	Window        model.Interval    `json:"window"`
	Operations    []string          `json:"operations"`
	Requirements  []ZoneRequirement `json:"requirements"`
	Satisfied     bool              `json:"satisfied"`
	Blocking      []string          `json:"blocking,omitempty"`
	UsedPermitIDs []string          `json:"used_permit_ids"`
}

// Assess evaluates the permit position for a window over the given zones.
func Assess(systemID string, zones []model.ProtectionZone, all []model.Permit, window model.Interval, ops []model.Operation) Assessment {
	out := Assessment{
		SystemID:   systemID,
		Window:     window,
		Operations: model.OperationNames(ops),
		Satisfied:  true,
	}
	sortedZones := make([]model.ProtectionZone, len(zones))
	copy(sortedZones, zones)
	sort.SliceStable(sortedZones, func(i, j int) bool { return sortedZones[i].ID < sortedZones[j].ID })
	used := map[string]bool{}
	for _, zone := range sortedZones {
		req := ZoneRequirement{
			ZoneID:         zone.ID,
			Jurisdiction:   zone.Jurisdiction,
			PermitRequired: zone.PermitRequired,
		}
		if !zone.PermitRequired {
			req.Satisfied = true
			req.Note = "zone does not require a permit"
			out.Requirements = append(out.Requirements, req)
			continue
		}
		candidates := model.PermitsForZone(all, systemID, zone.ID)
		if len(candidates) == 0 {
			req.Note = "no permit exists for this zone"
			out.Satisfied = false
			out.Blocking = append(out.Blocking, fmt.Sprintf("zone %s has no permit", zone.ID))
			out.Requirements = append(out.Requirements, req)
			continue
		}
		for _, permit := range candidates {
			status := PermitStatus{
				PermitID:     permit.ID,
				Reference:    permit.Reference,
				ValidFrom:    permit.ValidFrom,
				ValidTo:      permit.ValidTo,
				Operations:   model.OperationNames(permit.Operations),
				CoversWindow: permit.Window().Contains(window),
			}
			var missing []string
			for _, op := range ops {
				if !permit.Allows(op) {
					missing = append(missing, string(op))
				}
			}
			sort.Strings(missing)
			status.MissingOperations = missing
			status.CoversOperations = len(missing) == 0
			req.Candidates = append(req.Candidates, status)
			if status.Usable() && req.CoveringPermitID == "" {
				req.CoveringPermitID = permit.ID
				req.Satisfied = true
			}
		}
		if !req.Satisfied {
			out.Satisfied = false
			out.Blocking = append(out.Blocking, fmt.Sprintf("zone %s is not covered for %s", zone.ID, window.Start))
			req.Note = "no candidate permit covers the window and every operation"
		} else {
			used[req.CoveringPermitID] = true
		}
		out.Requirements = append(out.Requirements, req)
	}
	for id := range used {
		out.UsedPermitIDs = append(out.UsedPermitIDs, id)
	}
	sort.Strings(out.UsedPermitIDs)
	sort.Strings(out.Blocking)
	return out
}

// RequiredZones filters the zones that need a permit.
func RequiredZones(zones []model.ProtectionZone) []model.ProtectionZone {
	out := make([]model.ProtectionZone, 0, len(zones))
	for _, z := range zones {
		if z.PermitRequired {
			out = append(out, z)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CandidateStarts returns the sorted, de-duplicated instants at which the permit
// position can change: the requested earliest start plus every permit validity
// boundary at or after it. Scheduling only needs to consider these instants.
func CandidateStarts(systemID string, zones []model.ProtectionZone, all []model.Permit, notBefore model.UTCTime) []model.UTCTime {
	seen := map[string]bool{}
	var out []model.UTCTime
	push := func(t model.UTCTime) {
		if t.IsZero() {
			return
		}
		if !notBefore.IsZero() && t.Before(notBefore) {
			return
		}
		if seen[t.String()] {
			return
		}
		seen[t.String()] = true
		out = append(out, t)
	}
	push(notBefore)
	for _, zone := range RequiredZones(zones) {
		for _, permit := range model.PermitsForZone(all, systemID, zone.ID) {
			push(permit.ValidFrom)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// Covers reports whether every permit-required zone authorizes the window.
func Covers(systemID string, zones []model.ProtectionZone, all []model.Permit, window model.Interval, ops []model.Operation) bool {
	return Assess(systemID, zones, all, window, ops).Satisfied
}

// LatestExpiry returns the last validity end across the permits covering the
// given zones, which bounds how far scheduling can be deferred.
func LatestExpiry(systemID string, zones []model.ProtectionZone, all []model.Permit) model.UTCTime {
	var latest model.UTCTime
	for _, zone := range RequiredZones(zones) {
		for _, permit := range model.PermitsForZone(all, systemID, zone.ID) {
			latest = model.MaxTime(latest, permit.ValidTo)
		}
	}
	return latest
}
