package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestValidateCollectsEveryProblem(t *testing.T) {
	cfg := Default()
	cfg.Version = 7
	cfg.StoreDir = ""
	cfg.Localization.DefaultSlackFactor = 0.5
	cfg.Repair.JointsPerRepair = 0
	cfg.Output.FloatDecimals = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	msg := err.Error()
	for _, want := range []string{"version", "store_dir", "default_slack_factor", "joints_per_repair", "float_decimals"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error should mention %q: %s", want, msg)
		}
	}
}

func TestLoadAppliesPartialDocumentOverDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"version":1,"repair":{"survey_hours":10.0},"output":{"float_decimals":2}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Repair.SurveyHours != 10 {
		t.Fatalf("survey hours = %v, want the override", cfg.Repair.SurveyHours)
	}
	if cfg.Repair.CutAndHoldHours != Default().Repair.CutAndHoldHours {
		t.Fatal("omitted fields must keep their default")
	}
	if cfg.Decimals() != 2 {
		t.Fatalf("decimals = %d, want 2", cfg.Decimals())
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"nope":true}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestLoadWithEmptyPathReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StoreDir != Default().StoreDir {
		t.Fatalf("store dir = %q", cfg.StoreDir)
	}
}

func TestOnSiteHoursIncludesBurialAndFloor(t *testing.T) {
	cfg := Default()
	base := cfg.Repair.SurveyHours + cfg.Repair.CutAndHoldHours + cfg.Repair.TestHours + 2*cfg.Repair.SpliceHoursPerJoint
	if got := cfg.OnSiteHours(0, 2); got != base {
		t.Fatalf("on-site hours = %v, want %v", got, base)
	}
	if got := cfg.OnSiteHours(4, 2); got != base+4*cfg.Repair.BurialHoursPerKm {
		t.Fatalf("burial hours not added: %v", got)
	}
	cfg.Repair.MinWorkHours = 500
	if got := cfg.OnSiteHours(0, 1); got != 500 {
		t.Fatalf("minimum work hours not applied: %v", got)
	}
}

func TestRoundUsesConfiguredPrecision(t *testing.T) {
	cfg := Default()
	cfg.Output.FloatDecimals = 2
	if got := cfg.Round(1.23456); got != 1.23 {
		t.Fatalf("round = %v, want 1.23", got)
	}
}
