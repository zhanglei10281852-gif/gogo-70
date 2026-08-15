package validate

import (
	"fmt"
	"sort"

	"CableMend/internal/model"
)

// Severity levels of a validation problem.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// Problem is one validation finding.
type Problem struct {
	Document string `json:"document"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

// DocumentSummary describes one loaded document.
type DocumentSummary struct {
	Document string `json:"document"`
	Path     string `json:"path"`
	Records  int    `json:"records"`
	Note     string `json:"note,omitempty"`
}

// Report is the validation outcome.
type Report struct {
	Sources   []string          `json:"sources"`
	Documents []DocumentSummary `json:"documents"`
	Checks    []string          `json:"checks"`
	Errors    []Problem         `json:"errors"`
	Warnings  []Problem         `json:"warnings"`
	OK        bool              `json:"ok"`
}

// Check performs every structural and cross-document validation.
func Check(in Inputs, l Loaded) Report {
	rep := Report{Sources: l.Sources}
	var errs, warns []Problem
	addErr := func(doc, format string, args ...any) {
		errs = append(errs, Problem{Document: doc, Severity: SeverityError, Detail: fmt.Sprintf(format, args...)})
	}
	addWarn := func(doc, format string, args ...any) {
		warns = append(warns, Problem{Document: doc, Severity: SeverityWarning, Detail: fmt.Sprintf(format, args...)})
	}

	rep.Checks = append(rep.Checks, "config field ranges")
	if err := l.Config.Validate(); err != nil {
		addErr("config", "%v", err)
	}

	knownSystems := map[string]bool{}
	if in.SystemsPath != "" {
		rep.Checks = append(rep.Checks, "cable system geometry, zones and inline assets")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "systems", Path: in.SystemsPath, Records: len(l.Systems.Systems),
		})
		for _, p := range l.Systems.Validate() {
			addErr("systems", "%s", p)
		}
		for _, sys := range l.Systems.Systems {
			knownSystems[sys.ID] = true
		}
	}

	if in.AssetsPath != "" {
		rep.Checks = append(rep.Checks, "vessel and depot definitions")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "assets", Path: in.AssetsPath,
			Records: len(l.Assets.Vessels) + len(l.Assets.Depots),
			Note:    fmt.Sprintf("%d vessels, %d depots", len(l.Assets.Vessels), len(l.Assets.Depots)),
		})
		for _, p := range l.Assets.Validate() {
			addErr("assets", "%s", p)
		}
		if len(knownSystems) > 0 {
			for _, depot := range l.Assets.Depots {
				for _, access := range depot.Access {
					if !knownSystems[access.SystemID] {
						addErr("assets", "depot %s references unknown system %q", depot.ID, access.SystemID)
					}
				}
			}
			for _, vessel := range l.Assets.Vessels {
				reachable := 0
				for _, depot := range l.Assets.Depots {
					if depot.ID == vessel.StationDepotID {
						reachable = len(depot.Access)
					}
				}
				if reachable == 0 {
					addWarn("assets", "vessel %s is stationed at a depot without route access entries", vessel.ID)
				}
			}
		}
	}

	if in.PermitsPath != "" {
		rep.Checks = append(rep.Checks, "permit validity intervals and operations")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "permits", Path: in.PermitsPath, Records: len(l.Permits),
		})
		doc := model.PermitsDocument{Version: model.PermitsSchemaVersion, Permits: l.Permits}
		for _, p := range doc.Validate() {
			addErr("permits", "%s", p)
		}
		if l.SystemSet != nil {
			for _, permit := range l.Permits {
				ix, ok := l.SystemSet.Get(permit.SystemID)
				if !ok {
					addErr("permits", "permit %s references unknown system %q", permit.ID, permit.SystemID)
					continue
				}
				found := false
				for _, zone := range ix.Zones() {
					if zone.ID == permit.ZoneID {
						found = true
						if zone.Jurisdiction != permit.Jurisdiction {
							addWarn("permits", "permit %s jurisdiction %q differs from zone %s jurisdiction %q",
								permit.ID, permit.Jurisdiction, zone.ID, zone.Jurisdiction)
						}
						break
					}
				}
				if !found {
					addErr("permits", "permit %s references unknown protection zone %q", permit.ID, permit.ZoneID)
				}
			}
			for _, id := range l.SystemSet.IDs() {
				ix, _ := l.SystemSet.Get(id)
				for _, zone := range ix.Zones() {
					if !zone.PermitRequired {
						continue
					}
					if len(model.PermitsForZone(l.Permits, id, zone.ID)) == 0 {
						addWarn("permits", "zone %s on system %s requires a permit but none is defined", zone.ID, id)
					}
				}
			}
		}
	}

	if in.EvidencePath != "" {
		rep.Checks = append(rep.Checks, "fault evidence records and measured distances")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "evidence", Path: in.EvidencePath, Records: len(l.Evidence),
		})
		for _, rec := range l.Evidence {
			for _, p := range rec.Validate() {
				addErr("evidence", "%s", p)
			}
			if l.SystemSet == nil {
				continue
			}
			ix, ok := l.SystemSet.Get(rec.SystemID)
			if !ok {
				addErr("evidence", "record %s references unknown system %q", rec.ID, rec.SystemID)
				continue
			}
			cableKm, err := rec.MeasuredCableKm()
			if err != nil {
				continue
			}
			if cableKm > ix.CableLengthKm() {
				addWarn("evidence", "record %s measures %.3f km of cable but system %s holds %.3f km",
					rec.ID, cableKm, ix.ID(), ix.CableLengthKm())
			}
		}
	}

	if in.WeatherPath != "" {
		rep.Checks = append(rep.Checks, "sea-state observation series")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "weather", Path: in.WeatherPath, Records: len(l.Observations),
		})
		bySystem := map[string]int{}
		for _, obs := range l.Observations {
			for _, p := range obs.Validate() {
				addErr("weather", "%s", p)
			}
			bySystem[obs.SystemID]++
			if l.SystemSet != nil {
				if _, ok := l.SystemSet.Get(obs.SystemID); !ok {
					addErr("weather", "observation at %s references unknown system %q", obs.HourStart, obs.SystemID)
				}
			}
		}
		for _, id := range sortedMapKeys(bySystem) {
			if bySystem[id] < 24 {
				addWarn("weather", "system %s has only %d observation hours", id, bySystem[id])
			}
		}
	}

	if in.FaultsPath != "" {
		rep.Checks = append(rep.Checks, "declared faults and their evidence coverage")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "faults", Path: in.FaultsPath, Records: len(l.Faults),
		})
		doc := model.FaultsDocument{Version: model.FaultsSchemaVersion, Faults: l.Faults}
		for _, p := range doc.Validate() {
			addErr("faults", "%s", p)
		}
		evidenceByFault := map[string]int{}
		for _, rec := range l.Evidence {
			evidenceByFault[rec.FaultID]++
		}
		for _, fault := range l.Faults {
			if l.SystemSet != nil {
				if _, ok := l.SystemSet.Get(fault.SystemID); !ok {
					addErr("faults", "fault %s references unknown system %q", fault.ID, fault.SystemID)
				}
			}
			if in.EvidencePath == "" {
				continue
			}
			if evidenceByFault[fault.ID] == 0 {
				addErr("faults", "fault %s has no evidence records", fault.ID)
			} else if evidenceByFault[fault.ID] < l.Config.Localization.MinEvidenceRecords {
				addWarn("faults", "fault %s has %d evidence records, below the configured minimum",
					fault.ID, evidenceByFault[fault.ID])
			}
		}
	}

	if in.VerificationPath != "" {
		rep.Checks = append(rep.Checks, "post-repair verification records")
		rep.Documents = append(rep.Documents, DocumentSummary{
			Document: "verification", Path: in.VerificationPath, Records: len(l.Verifications),
		})
		for _, rec := range l.Verifications {
			for _, p := range rec.Validate() {
				addErr("verification", "%s", p)
			}
			if l.SystemSet == nil {
				continue
			}
			ix, ok := l.SystemSet.Get(rec.SystemID)
			if !ok {
				addErr("verification", "record %s references unknown system %q", rec.ID, rec.SystemID)
				continue
			}
			if _, ok := ix.SegmentByID(rec.SegmentID); !ok {
				addErr("verification", "record %s references unknown segment %q", rec.ID, rec.SegmentID)
			}
			if rec.RepairKP < ix.RouteStartKP() || rec.RepairKP > ix.RouteEndKP() {
				addErr("verification", "record %s repair_kp %.3f is outside the route", rec.ID, rec.RepairKP)
			}
		}
	}

	sortProblems(errs)
	sortProblems(warns)
	rep.Errors = errs
	rep.Warnings = warns
	rep.OK = len(errs) == 0
	sort.Strings(rep.Checks)
	sort.SliceStable(rep.Documents, func(i, j int) bool { return rep.Documents[i].Document < rep.Documents[j].Document })
	return rep
}

func sortProblems(problems []Problem) {
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Document != problems[j].Document {
			return problems[i].Document < problems[j].Document
		}
		return problems[i].Detail < problems[j].Detail
	})
}

func sortedMapKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
