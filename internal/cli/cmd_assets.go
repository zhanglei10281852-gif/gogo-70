package cli

import (
	"flag"
	"fmt"
	"io"

	"CableMend/internal/model"
	"CableMend/internal/numeric"
	"CableMend/internal/validate"
)

// AccessView is a depot access entry with the distance to the requested site.
type AccessView struct {
	SystemID       string  `json:"system_id"`
	ReferenceKP    float64 `json:"reference_kp"`
	DistanceNm     float64 `json:"distance_nm"`
	ToSiteNm       float64 `json:"to_site_nm,omitempty"`
	ToSiteRelevant bool    `json:"to_site_relevant"`
}

// DepotView is the depot rendering.
type DepotView struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Jurisdiction string       `json:"jurisdiction"`
	SpareCableKm float64      `json:"spare_cable_km"`
	SpareJoints  int          `json:"spare_joints"`
	Access       []AccessView `json:"access"`
}

// VesselView is the vessel rendering.
type VesselView struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	StationDepotID    string        `json:"station_depot_id"`
	TransitSpeedKn    float64       `json:"transit_speed_kn"`
	MobilizationHours float64       `json:"mobilization_hours"`
	SpareCableKm      float64       `json:"spare_cable_km"`
	SpareJoints       int           `json:"spare_joints"`
	HasROV            bool          `json:"has_rov"`
	HasAUV            bool          `json:"has_auv"`
	MaxWorkingDepthM  float64       `json:"max_working_depth_m"`
	MaxSeaStateM      float64       `json:"max_sea_state_m"`
	DayRateUnits      float64       `json:"day_rate_units"`
	AvailableFrom     model.UTCTime `json:"available_from"`
	TransitNm         float64       `json:"transit_nm,omitempty"`
	TransitHours      float64       `json:"transit_hours,omitempty"`
	DepthCapable      bool          `json:"depth_capable"`
	Notes             []string      `json:"notes,omitempty"`
}

// AssetsOutput is the assets command document.
type AssetsOutput struct {
	SiteRequested bool         `json:"site_requested"`
	Site          model.Site   `json:"site"`
	SiteDepthM    float64      `json:"site_depth_m,omitempty"`
	DepthKnown    bool         `json:"site_depth_known"`
	Vessels       []VesselView `json:"vessels"`
	Depots        []DepotView  `json:"depots"`
}

// cmdAssets lists the repair asset pool, optionally scored against a work site.
func cmdAssets(e env, args []string) error {
	var (
		c        common
		in       validate.Inputs
		systemID string
		kp       float64
		fs       = flag.NewFlagSet("assets", flag.ContinueOnError)
	)
	c.register(fs)
	fs.StringVar(&in.AssetsPath, "assets", "", "repair assets JSON document")
	fs.StringVar(&in.SystemsPath, "systems", "", "cable systems JSON document")
	fs.StringVar(&systemID, "system", "", "score the assets against this system id")
	fs.Float64Var(&kp, "kp", -1, "route position in kilometres for the scored site")
	if err := parse(fs, e, args); err != nil {
		return err
	}
	if err := c.validateFormat(); err != nil {
		return err
	}
	if err := requireFlag("assets", in.AssetsPath); err != nil {
		return err
	}
	if err := requireFlag("systems", in.SystemsPath); err != nil {
		return err
	}
	if systemID != "" && kp < 0 {
		return fmt.Errorf("--kp is required when --system is given")
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

	out := AssetsOutput{}
	var site model.Site
	if systemID != "" {
		ix, err := loaded.SystemSet.MustGet(systemID)
		if err != nil {
			return err
		}
		if kp < ix.RouteStartKP() || kp > ix.RouteEndKP() {
			return fmt.Errorf("KP %g is outside system %s route [%g, %g]", kp, systemID, ix.RouteStartKP(), ix.RouteEndKP())
		}
		site = model.Site{SystemID: systemID, KP: cfg.Round(kp)}
		out.SiteRequested = true
		out.Site = site
		depth := ix.DepthAt(kp)
		out.DepthKnown = depth.Known
		out.SiteDepthM = cfg.Round(depth.DepthM)
	}

	for _, depot := range loaded.AssetSet.Depots() {
		view := DepotView{
			ID:           depot.ID,
			Name:         depot.Name,
			Jurisdiction: depot.Jurisdiction,
			SpareCableKm: cfg.Round(depot.SpareCableKm),
			SpareJoints:  depot.SpareJoints,
		}
		for _, access := range depot.Access {
			entry := AccessView{
				SystemID:    access.SystemID,
				ReferenceKP: cfg.Round(access.ReferenceKP),
				DistanceNm:  cfg.Round(access.DistanceNm),
			}
			if out.SiteRequested && access.SystemID == site.SystemID {
				if dist, err := depot.DistanceNmToSite(site); err == nil {
					entry.ToSiteNm = cfg.Round(dist)
					entry.ToSiteRelevant = true
				}
			}
			view.Access = append(view.Access, entry)
		}
		out.Depots = append(out.Depots, view)
	}

	for _, vessel := range loaded.AssetSet.Vessels() {
		view := VesselView{
			ID:                vessel.ID,
			Name:              vessel.Name,
			StationDepotID:    vessel.StationDepotID,
			TransitSpeedKn:    cfg.Round(vessel.TransitSpeedKn),
			MobilizationHours: cfg.Round(vessel.MobilizationHours),
			SpareCableKm:      cfg.Round(vessel.SpareCableKm),
			SpareJoints:       vessel.SpareJoints,
			HasROV:            vessel.HasROV,
			HasAUV:            vessel.HasAUV,
			MaxWorkingDepthM:  cfg.Round(vessel.MaxWorkingDepthM),
			MaxSeaStateM:      cfg.Round(vessel.MaxSeaStateM),
			DayRateUnits:      cfg.Round(vessel.DayRateUnits),
			AvailableFrom:     vessel.AvailableFrom,
			DepthCapable:      true,
		}
		if !vessel.HasROV {
			view.Notes = append(view.Notes, "no remotely operated vehicle embarked")
		}
		if !vessel.HasAUV {
			view.Notes = append(view.Notes, "no autonomous survey vehicle embarked")
		}
		if out.SiteRequested {
			depot, ok := loaded.AssetSet.Depot(vessel.StationDepotID)
			if ok {
				if dist, err := depot.DistanceNmToSite(site); err == nil {
					view.TransitNm = cfg.Round(dist)
					if hours, err := numeric.TransitHours(dist, vessel.TransitSpeedKn); err == nil {
						view.TransitHours = cfg.Round(hours)
					}
				} else {
					view.Notes = append(view.Notes, "station depot has no access to the requested system")
				}
			}
			if out.DepthKnown && out.SiteDepthM+cfg.Repair.DepthMarginM > vessel.MaxWorkingDepthM {
				view.DepthCapable = false
				view.Notes = append(view.Notes, "site depth plus margin exceeds the working depth limit")
			}
		}
		out.Vessels = append(out.Vessels, view)
	}

	return c.emit(e, out, func(w io.Writer) error {
		return renderAssets(w, cfg.Decimals(), out)
	})
}

// renderAssets prints the asset pool as text.
func renderAssets(w io.Writer, decimals int, out AssetsOutput) error {
	t := newText(w, decimals)
	t.heading("repair assets")
	if out.SiteRequested {
		t.kv("scored site", fmt.Sprintf("%s at KP %s", out.Site.SystemID, t.f(out.Site.KP)))
		if out.DepthKnown {
			t.num("site depth m", out.SiteDepthM)
		} else {
			t.kv("site depth m", "unknown")
		}
	} else {
		t.kv("scored site", "none (pass --system and --kp)")
	}
	t.blank()

	t.heading("vessels")
	rows := make([][]string, 0, len(out.Vessels))
	for _, v := range out.Vessels {
		rows = append(rows, []string{
			v.ID,
			v.Name,
			v.StationDepotID,
			t.f(v.TransitSpeedKn),
			t.f(v.MobilizationHours),
			t.f(v.SpareCableKm),
			fmt.Sprintf("%d", v.SpareJoints),
			boolWord(v.HasROV),
			boolWord(v.HasAUV),
			t.f(v.MaxWorkingDepthM),
			t.f(v.MaxSeaStateM),
			v.AvailableFrom.String(),
			t.f(v.TransitNm),
			boolWord(v.DepthCapable),
		})
	}
	t.table([]string{"vessel", "name", "depot", "kn", "mob h", "cable km", "joints", "rov", "auv", "max m", "sea m", "available", "nm", "depth ok"}, rows)
	t.blank()

	for _, v := range out.Vessels {
		if len(v.Notes) == 0 {
			continue
		}
		t.list("note "+v.ID, v.Notes)
	}
	t.blank()

	t.heading("depots")
	depotRows := make([][]string, 0, len(out.Depots))
	for _, d := range out.Depots {
		depotRows = append(depotRows, []string{
			d.ID, d.Name, d.Jurisdiction, t.f(d.SpareCableKm), fmt.Sprintf("%d", d.SpareJoints), fmt.Sprintf("%d", len(d.Access)),
		})
	}
	t.table([]string{"depot", "name", "jurisdiction", "cable km", "joints", "routes"}, depotRows)
	t.blank()

	t.heading("depot access")
	accessRows := make([][]string, 0)
	for _, d := range out.Depots {
		for _, a := range d.Access {
			toSite := "-"
			if a.ToSiteRelevant {
				toSite = t.f(a.ToSiteNm)
			}
			accessRows = append(accessRows, []string{d.ID, a.SystemID, t.f(a.ReferenceKP), t.f(a.DistanceNm), toSite})
		}
	}
	t.table([]string{"depot", "system", "reference kp", "distance nm", "to site nm"}, accessRows)
	return t.done()
}
