// Package validate loads every CableMend input document strictly and checks it,
// both on its own and against the other documents. Loading never guesses: a
// malformed document is an error, and a structurally valid but inconsistent set
// of documents produces a problem report.
package validate

import (
	"fmt"
	"sort"
	"strings"

	"CableMend/internal/config"
	"CableMend/internal/jsonio"
	"CableMend/internal/model"
)

// Inputs lists the optional paths of the input documents.
type Inputs struct {
	ConfigPath       string
	SystemsPath      string
	EvidencePath     string
	AssetsPath       string
	WeatherPath      string
	PermitsPath      string
	FaultsPath       string
	VerificationPath string
}

// Loaded is the decoded input set.
type Loaded struct {
	Config        config.Config
	Systems       model.SystemsDocument
	SystemSet     *model.SystemSet
	Evidence      []model.Evidence
	Assets        model.AssetsDocument
	AssetSet      *model.AssetSet
	Observations  []model.Observation
	Permits       []model.Permit
	Faults        []model.FaultRecord
	Verifications []model.VerificationRecord
	Sources       []string
}

// Load decodes every provided document.
func Load(in Inputs) (Loaded, error) {
	var out Loaded
	cfg, err := config.Load(in.ConfigPath)
	if err != nil {
		return Loaded{}, err
	}
	out.Config = cfg
	if in.ConfigPath != "" {
		out.Sources = append(out.Sources, "config="+in.ConfigPath)
	}
	if in.SystemsPath != "" {
		if err := jsonio.DecodeFile(in.SystemsPath, &out.Systems); err != nil {
			return Loaded{}, err
		}
		out.SystemSet = model.NewSystemSet(out.Systems, cfg.Localization.DefaultSlackFactor)
		out.Sources = append(out.Sources, "systems="+in.SystemsPath)
	}
	if in.AssetsPath != "" {
		if err := jsonio.DecodeFile(in.AssetsPath, &out.Assets); err != nil {
			return Loaded{}, err
		}
		out.AssetSet = model.NewAssetSet(out.Assets)
		out.Sources = append(out.Sources, "assets="+in.AssetsPath)
	}
	if in.PermitsPath != "" {
		var doc model.PermitsDocument
		if err := jsonio.DecodeFile(in.PermitsPath, &doc); err != nil {
			return Loaded{}, err
		}
		if doc.Version != model.PermitsSchemaVersion {
			return Loaded{}, fmt.Errorf("%s: permits version must be %d, got %d",
				in.PermitsPath, model.PermitsSchemaVersion, doc.Version)
		}
		out.Permits = model.SortPermits(doc.Permits)
		out.Sources = append(out.Sources, "permits="+in.PermitsPath)
	}
	if in.FaultsPath != "" {
		var doc model.FaultsDocument
		if err := jsonio.DecodeFile(in.FaultsPath, &doc); err != nil {
			return Loaded{}, err
		}
		if doc.Version != model.FaultsSchemaVersion {
			return Loaded{}, fmt.Errorf("%s: faults version must be %d, got %d",
				in.FaultsPath, model.FaultsSchemaVersion, doc.Version)
		}
		out.Faults = model.SortFaults(doc.Faults)
		out.Sources = append(out.Sources, "faults="+in.FaultsPath)
	}
	if in.EvidencePath != "" {
		records, err := LoadEvidence(in.EvidencePath)
		if err != nil {
			return Loaded{}, err
		}
		out.Evidence = records
		out.Sources = append(out.Sources, "evidence="+in.EvidencePath)
	}
	if in.WeatherPath != "" {
		obs, err := LoadObservations(in.WeatherPath)
		if err != nil {
			return Loaded{}, err
		}
		out.Observations = obs
		out.Sources = append(out.Sources, "weather="+in.WeatherPath)
	}
	if in.VerificationPath != "" {
		records, err := LoadVerifications(in.VerificationPath)
		if err != nil {
			return Loaded{}, err
		}
		out.Verifications = records
		out.Sources = append(out.Sources, "verification="+in.VerificationPath)
	}
	sort.Strings(out.Sources)
	return out, nil
}

// LoadEvidence decodes an evidence JSONL file.
func LoadEvidence(path string) ([]model.Evidence, error) {
	var out []model.Evidence
	seen := map[string]bool{}
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var rec model.Evidence
		if err := jsonio.DecodeRecord(raw, &rec); err != nil {
			return err
		}
		if strings.TrimSpace(rec.ID) == "" {
			return fmt.Errorf("evidence record has no id")
		}
		if seen[rec.ID] {
			return fmt.Errorf("duplicate evidence id %q", rec.ID)
		}
		seen[rec.ID] = true
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.SortEvidence(out), nil
}

// LoadObservations decodes a sea-state JSONL file.
func LoadObservations(path string) ([]model.Observation, error) {
	var out []model.Observation
	seen := map[string]bool{}
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var rec model.Observation
		if err := jsonio.DecodeRecord(raw, &rec); err != nil {
			return err
		}
		key := rec.SystemID + "|" + rec.HourStart.String()
		if seen[key] {
			return fmt.Errorf("duplicate observation for system %s at %s", rec.SystemID, rec.HourStart)
		}
		seen[key] = true
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.SortObservations(out), nil
}

// LoadVerifications decodes a verification JSONL file.
func LoadVerifications(path string) ([]model.VerificationRecord, error) {
	var out []model.VerificationRecord
	seen := map[string]bool{}
	err := jsonio.ForEachLine(path, func(_ int, raw []byte) error {
		var rec model.VerificationRecord
		if err := jsonio.DecodeRecord(raw, &rec); err != nil {
			return err
		}
		if seen[rec.ID] {
			return fmt.Errorf("duplicate verification id %q", rec.ID)
		}
		seen[rec.ID] = true
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.SortVerifications(out), nil
}
