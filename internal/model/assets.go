package model

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/numeric"
)

// AssetsSchemaVersion is the accepted version of an assets document.
const AssetsSchemaVersion = 1

// AssetsDocument is the repair asset input file.
type AssetsDocument struct {
	Version int      `json:"version"`
	Vessels []Vessel `json:"vessels"`
	Depots  []Depot  `json:"depots"`
}

// Vessel is a fictional cable repair ship.
type Vessel struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	HomeDepotID       string  `json:"home_depot_id"`
	StationDepotID    string  `json:"station_depot_id"`
	TransitSpeedKn    float64 `json:"transit_speed_kn"`
	MobilizationHours float64 `json:"mobilization_hours"`
	SpareCableKm      float64 `json:"spare_cable_km"`
	SpareJoints       int     `json:"spare_joints"`
	HasROV            bool    `json:"has_rov"`
	HasAUV            bool    `json:"has_auv"`
	MaxWorkingDepthM  float64 `json:"max_working_depth_m"`
	MaxSeaStateM      float64 `json:"max_sea_state_m"`
	DayRateUnits      float64 `json:"day_rate_units"`
	AvailableFrom     UTCTime `json:"available_from"`
}

// Depot is a shore store holding spare cable and joints.
type Depot struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Jurisdiction string        `json:"jurisdiction"`
	SpareCableKm float64       `json:"spare_cable_km"`
	SpareJoints  int           `json:"spare_joints"`
	Access       []DepotAccess `json:"access"`
}

// DepotAccess is the sailing distance from a depot to a reference position on a
// cable route. Distances to other positions on the same route are derived from
// the along-route offset.
type DepotAccess struct {
	SystemID    string  `json:"system_id"`
	ReferenceKP float64 `json:"reference_kp"`
	DistanceNm  float64 `json:"distance_nm"`
}

// Site is a work position on a cable route.
type Site struct {
	SystemID string  `json:"system_id"`
	KP       float64 `json:"kp"`
}

// String renders a site compactly for diagnostics.
func (s Site) String() string { return fmt.Sprintf("%s@KP%.3f", s.SystemID, s.KP) }

// Validate checks the assets document.
func (d AssetsDocument) Validate() []string {
	var problems []string
	if d.Version != AssetsSchemaVersion {
		problems = append(problems, fmt.Sprintf("assets: version must be %d, got %d", AssetsSchemaVersion, d.Version))
	}
	if len(d.Vessels) == 0 {
		problems = append(problems, "assets: at least one vessel is required")
	}
	if len(d.Depots) == 0 {
		problems = append(problems, "assets: at least one depot is required")
	}
	depotIDs := make(map[string]bool, len(d.Depots))
	for _, dep := range d.Depots {
		if strings.TrimSpace(dep.ID) == "" {
			problems = append(problems, "assets: depot id must not be empty")
			continue
		}
		if depotIDs[dep.ID] {
			problems = append(problems, fmt.Sprintf("assets: duplicate depot id %q", dep.ID))
		}
		depotIDs[dep.ID] = true
		problems = append(problems, dep.Validate()...)
	}
	vesselIDs := make(map[string]bool, len(d.Vessels))
	for _, v := range d.Vessels {
		if strings.TrimSpace(v.ID) == "" {
			problems = append(problems, "assets: vessel id must not be empty")
			continue
		}
		if vesselIDs[v.ID] {
			problems = append(problems, fmt.Sprintf("assets: duplicate vessel id %q", v.ID))
		}
		vesselIDs[v.ID] = true
		problems = append(problems, v.Validate()...)
		if v.HomeDepotID != "" && !depotIDs[v.HomeDepotID] {
			problems = append(problems, fmt.Sprintf("vessel %s: home_depot_id %q is not a known depot", v.ID, v.HomeDepotID))
		}
		if v.StationDepotID != "" && !depotIDs[v.StationDepotID] {
			problems = append(problems, fmt.Sprintf("vessel %s: station_depot_id %q is not a known depot", v.ID, v.StationDepotID))
		}
	}
	sort.Strings(problems)
	return problems
}

// Validate checks one depot.
func (d Depot) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("depot %s: %s", d.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(d.Name) == "" {
		add("name must not be empty")
	}
	if strings.TrimSpace(d.Jurisdiction) == "" {
		add("jurisdiction must not be empty")
	}
	if !numeric.NonNegative(d.SpareCableKm) {
		add("spare_cable_km must not be negative")
	}
	if d.SpareJoints < 0 {
		add("spare_joints must not be negative")
	}
	if len(d.Access) == 0 {
		add("at least one access entry is required")
	}
	seen := make(map[string]bool, len(d.Access))
	for _, a := range d.Access {
		if strings.TrimSpace(a.SystemID) == "" {
			add("access entry system_id must not be empty")
			continue
		}
		if seen[a.SystemID] {
			add("duplicate access entry for system %s", a.SystemID)
		}
		seen[a.SystemID] = true
		if !numeric.NonNegative(a.DistanceNm) {
			add("access distance_nm for system %s must not be negative", a.SystemID)
		}
		if !numeric.NonNegative(a.ReferenceKP) {
			add("access reference_kp for system %s must not be negative", a.SystemID)
		}
	}
	return problems
}

// Validate checks one vessel.
func (v Vessel) Validate() []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("vessel %s: %s", v.ID, fmt.Sprintf(format, args...)))
	}
	if strings.TrimSpace(v.Name) == "" {
		add("name must not be empty")
	}
	if strings.TrimSpace(v.StationDepotID) == "" {
		add("station_depot_id must not be empty")
	}
	if !numeric.Positive(v.TransitSpeedKn) {
		add("transit_speed_kn must be positive")
	}
	if !numeric.NonNegative(v.MobilizationHours) {
		add("mobilization_hours must not be negative")
	}
	if !numeric.NonNegative(v.SpareCableKm) {
		add("spare_cable_km must not be negative")
	}
	if v.SpareJoints < 0 {
		add("spare_joints must not be negative")
	}
	if !numeric.Positive(v.MaxWorkingDepthM) {
		add("max_working_depth_m must be positive")
	}
	if !numeric.Positive(v.MaxSeaStateM) {
		add("max_sea_state_m must be positive")
	}
	if !numeric.NonNegative(v.DayRateUnits) {
		add("day_rate_units must not be negative")
	}
	if v.AvailableFrom.IsZero() {
		add("available_from must be set")
	}
	return problems
}

// AssetSet is the indexed asset pool.
type AssetSet struct {
	vessels []Vessel
	depots  []Depot
	byDepot map[string]Depot
}

// NewAssetSet indexes an assets document with deterministic ordering.
func NewAssetSet(doc AssetsDocument) *AssetSet {
	set := &AssetSet{byDepot: make(map[string]Depot, len(doc.Depots))}
	set.vessels = append(set.vessels, doc.Vessels...)
	set.depots = append(set.depots, doc.Depots...)
	sort.SliceStable(set.vessels, func(i, j int) bool { return set.vessels[i].ID < set.vessels[j].ID })
	sort.SliceStable(set.depots, func(i, j int) bool { return set.depots[i].ID < set.depots[j].ID })
	for _, d := range set.depots {
		set.byDepot[d.ID] = d
	}
	return set
}

// Vessels returns the vessels ordered by id.
func (a *AssetSet) Vessels() []Vessel {
	out := make([]Vessel, len(a.vessels))
	copy(out, a.vessels)
	return out
}

// Depots returns the depots ordered by id.
func (a *AssetSet) Depots() []Depot {
	out := make([]Depot, len(a.depots))
	copy(out, a.depots)
	return out
}

// Depot looks a depot up by id.
func (a *AssetSet) Depot(id string) (Depot, bool) {
	d, ok := a.byDepot[id]
	return d, ok
}

// Vessel looks a vessel up by id.
func (a *AssetSet) Vessel(id string) (Vessel, bool) {
	for _, v := range a.vessels {
		if v.ID == id {
			return v, true
		}
	}
	return Vessel{}, false
}

// AccessFor returns the depot access entry for a system.
func (d Depot) AccessFor(systemID string) (DepotAccess, bool) {
	for _, a := range d.Access {
		if a.SystemID == systemID {
			return a, true
		}
	}
	return DepotAccess{}, false
}

// DistanceNmToSite returns the sailing distance from a depot to a work site.
func (d Depot) DistanceNmToSite(site Site) (float64, error) {
	access, ok := d.AccessFor(site.SystemID)
	if !ok {
		return 0, fmt.Errorf("depot %s has no access entry for system %s", d.ID, site.SystemID)
	}
	offset := abs(site.KP - access.ReferenceKP)
	return access.DistanceNm + numeric.KmToNm(offset), nil
}

// DepotDistanceNm returns the sailing distance between two depots, derived from
// the shortest connection through a route reference point both depots can reach.
// The connecting system id is returned for traceability.
func (a *AssetSet) DepotDistanceNm(fromID, toID string) (float64, string, error) {
	if fromID == toID {
		return 0, "", nil
	}
	from, ok := a.Depot(fromID)
	if !ok {
		return 0, "", fmt.Errorf("unknown depot %q", fromID)
	}
	to, ok := a.Depot(toID)
	if !ok {
		return 0, "", fmt.Errorf("unknown depot %q", toID)
	}
	bestNm := 0.0
	bestSystem := ""
	for _, access := range from.Access {
		other, ok := to.AccessFor(access.SystemID)
		if !ok {
			continue
		}
		total := access.DistanceNm + other.DistanceNm + numeric.KmToNm(abs(access.ReferenceKP-other.ReferenceKP))
		if bestSystem == "" || total < bestNm-1e-9 {
			bestNm, bestSystem = total, access.SystemID
		}
	}
	if bestSystem == "" {
		return 0, "", fmt.Errorf("depots %s and %s share no reachable system", fromID, toID)
	}
	return bestNm, bestSystem, nil
}

// RepositionNm returns the sailing distance between two work sites. Positions on
// the same route are connected along the route; positions on different routes
// are connected through the cheapest depot that can reach both, with the depot
// id as the tie-break.
func (a *AssetSet) RepositionNm(from, to Site) (float64, string, error) {
	if from.SystemID == to.SystemID {
		return numeric.KmToNm(abs(to.KP - from.KP)), "", nil
	}
	bestNm := 0.0
	bestDepot := ""
	for _, d := range a.depots {
		legOne, err := d.DistanceNmToSite(from)
		if err != nil {
			continue
		}
		legTwo, err := d.DistanceNmToSite(to)
		if err != nil {
			continue
		}
		total := legOne + legTwo
		if bestDepot == "" || total < bestNm-1e-9 {
			bestNm, bestDepot = total, d.ID
		}
	}
	if bestDepot == "" {
		return 0, "", fmt.Errorf("no depot connects system %s and system %s", from.SystemID, to.SystemID)
	}
	return bestNm, bestDepot, nil
}
