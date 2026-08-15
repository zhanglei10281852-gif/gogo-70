package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"CableMend/internal/model"
)

const fixtureSystems = `{
  "version": 1,
  "systems": [
    {
      "id": "SYS-T",
      "name": "Test Link",
      "slack_factor": 1.05,
      "traffic_tbps": 8.0,
      "landing_stations": [
        {"id": "LS-A", "name": "A", "kp": 0.0, "jurisdiction": "JA"},
        {"id": "LS-B", "name": "B", "kp": 100.0, "jurisdiction": "JB"}
      ],
      "segments": [
        {
          "id": "SEG-1", "start_kp": 0.0, "end_kp": 100.0, "cable_type": "da",
          "slack_factor": 1.05, "existing_joints": 0, "loss_db_per_km": 0.2,
          "burial_profile": [
            {"start_kp": 0.0, "end_kp": 30.0, "depth_m": 80.0, "burial_depth_m": 1.2, "seabed": "sand"},
            {"start_kp": 30.0, "end_kp": 100.0, "depth_m": 2400.0, "burial_depth_m": 0.0, "seabed": "clay"}
          ]
        }
      ],
      "repeaters": [{"id": "REP-1", "kp": 50.0, "kind": "repeater", "gain_db": 10.0, "cable_allowance_km": 0.05}],
      "branching_units": [],
      "protection_zones": [
        {"id": "PZ-1", "start_kp": 0.0, "end_kp": 35.0, "jurisdiction": "JA",
         "permit_required": true, "anchoring_restricted": true, "risk_weight": 2.0}
      ]
    }
  ]
}`

const fixtureEvidence = `{"id":"EV-1","system_id":"SYS-T","fault_id":"F-1","observed_at":"2026-03-10T01:00:00Z","end":"A","method":"otdr","cable_distance_km":21.0,"loss_step_db":3.0,"insulation_resistance_mohm":0.4}
{"id":"EV-2","system_id":"SYS-T","fault_id":"F-1","observed_at":"2026-03-10T01:10:00Z","end":"B","method":"otdr","cable_distance_km":84.05,"loss_step_db":3.1,"insulation_resistance_mohm":0.5}
`

const fixtureAssets = `{
  "version": 1,
  "vessels": [
    {"id":"CS-ONE","name":"One","home_depot_id":"DEP-A","station_depot_id":"DEP-A","transit_speed_kn":13.0,
     "mobilization_hours":12.0,"spare_cable_km":40.0,"spare_joints":8,"has_rov":true,"has_auv":true,
     "max_working_depth_m":3000.0,"max_sea_state_m":2.5,"day_rate_units":100.0,"available_from":"2026-03-10T00:00:00Z"}
  ],
  "depots": [
    {"id":"DEP-A","name":"Alpha","jurisdiction":"JA","spare_cable_km":200.0,"spare_joints":20,
     "access":[{"system_id":"SYS-T","reference_kp":0.0,"distance_nm":25.0}]}
  ]
}`

const fixturePermits = `{
  "version": 1,
  "permits": [
    {"id":"PMT-1","system_id":"SYS-T","zone_id":"PZ-1","jurisdiction":"JA",
     "valid_from":"2026-03-01T00:00:00Z","valid_to":"2026-04-01T00:00:00Z",
     "operations":["survey","cut_and_hold","splice","test","burial"],"reference":"JA/1"}
  ]
}`

const fixtureFaults = `{
  "version": 1,
  "faults": [
    {"id":"F-1","system_id":"SYS-T","reported_at":"2026-03-10T00:30:00Z","declared_priority":70.0,
     "traffic_tbps":8.0,"single_route":false,"restore_by":"2026-03-25T00:00:00Z","description":"test fault"}
  ]
}`

// fixtureFiles writes a complete offline input set and returns the directory.
func fixtureFiles(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	paths := map[string]string{}
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		paths[name] = path
	}
	write("systems.json", fixtureSystems)
	write("evidence.jsonl", fixtureEvidence)
	write("assets.json", fixtureAssets)
	write("permits.json", fixturePermits)
	write("faults.json", fixtureFaults)

	var weather strings.Builder
	start := model.MustParseUTC("2026-03-10T00:00:00Z")
	for i := 0; i < 240; i++ {
		wave := 1.1
		if i < 6 {
			wave = 3.4
		}
		weather.WriteString(fmt.Sprintf(
			`{"system_id":"SYS-T","hour_start":"%s","wave_height_m":%.2f,"wind_speed_kn":12.00,"visibility_km":12.00,"current_kn":0.40,"source":"T"}`+"\n",
			start.AddHours(float64(i)), wave))
	}
	write("weather.jsonl", weather.String())

	verification := fmt.Sprintf(
		`{"id":"VR-1","system_id":"SYS-T","segment_id":"SEG-1","fault_id":"F-1","measured_at":"2026-03-16T00:00:00Z","measured_loss_db":%.3f,"joints_after_repair":2,"repair_kp":20.0,"spare_cable_used_km":8.0,"burial_achieved_m":1.2,"rov_inspection":true,"notes":"ok"}`+"\n",
		(100*1.05+0.05)*0.2+2*0.15)
	write("verification.jsonl", verification)
	paths["store"] = filepath.Join(dir, "store")
	return dir, paths
}

// run executes the CLI and returns the exit code with both streams.
func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(&stdout, &stderr, args)
	return code, stdout.String(), stderr.String()
}

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	code, _, stderr := run()
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage: cablemend") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunHelpAndVersion(t *testing.T) {
	code, stdout, _ := run("help")
	if code != 0 || !strings.Contains(stdout, "campaign") {
		t.Fatalf("help exit %d, output %q", code, stdout)
	}
	code, stdout, _ = run("version")
	if code != 0 || !strings.Contains(stdout, Version) {
		t.Fatalf("version exit %d, output %q", code, stdout)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	code, _, stderr := run("frobnicate")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestValidateAcceptsTheFixture(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, stdout, stderr := run("validate",
		"--systems", paths["systems.json"],
		"--evidence", paths["evidence.jsonl"],
		"--assets", paths["assets.json"],
		"--weather", paths["weather.jsonl"],
		"--permits", paths["permits.json"],
		"--faults", paths["faults.json"],
		"--verification", paths["verification.jsonl"])
	if code != 0 {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "ok:                        yes") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestValidateReportsMissingRequiredFlag(t *testing.T) {
	code, _, stderr := run("validate")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "--systems is required") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestValidateFailsOnBrokenInput(t *testing.T) {
	dir, paths := fixtureFiles(t)
	broken := filepath.Join(dir, "broken.jsonl")
	if err := os.WriteFile(broken, []byte(`{"id":"EV-9","system_id":"SYS-NOPE","fault_id":"F-1","observed_at":"2026-03-10T01:00:00Z","end":"A","method":"otdr","cable_distance_km":10.0}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, stdout, _ := run("validate", "--systems", paths["systems.json"], "--evidence", broken)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "unknown system") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestLocateJSONOutputIsDeterministic(t *testing.T) {
	_, paths := fixtureFiles(t)
	args := []string{"locate", "--systems", paths["systems.json"], "--evidence", paths["evidence.jsonl"], "--format", "json"}
	codeOne, first, _ := run(args...)
	codeTwo, second, _ := run(args...)
	if codeOne != 0 || codeTwo != 0 {
		t.Fatalf("exit codes %d, %d", codeOne, codeTwo)
	}
	if first != second {
		t.Fatal("repeated runs must produce identical JSON")
	}
	if !strings.Contains(first, `"best_kp"`) {
		t.Fatalf("output = %s", first)
	}
}

func TestIngestThenReportVerifiesTheAuditChain(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, stdout, stderr := run("ingest", "--store", paths["store"],
		"--systems", paths["systems.json"], "--evidence", paths["evidence.jsonl"])
	if code != 0 {
		t.Fatalf("ingest exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "appended:                  2") {
		t.Fatalf("ingest stdout = %s", stdout)
	}
	code, stdout, stderr = run("ingest", "--store", paths["store"],
		"--systems", paths["systems.json"], "--evidence", paths["evidence.jsonl"])
	if code != 0 {
		t.Fatalf("second ingest exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "duplicates skipped:        2") {
		t.Fatalf("second ingest stdout = %s", stdout)
	}
	code, stdout, stderr = run("report", "--store", paths["store"], "--format", "json")
	if code != 0 {
		t.Fatalf("report exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, `"valid": true`) {
		t.Fatalf("report stdout = %s", stdout)
	}
}

func TestLocateFromStoreAfterIngest(t *testing.T) {
	_, paths := fixtureFiles(t)
	if code, _, stderr := run("ingest", "--store", paths["store"],
		"--systems", paths["systems.json"], "--evidence", paths["evidence.jsonl"]); code != 0 {
		t.Fatalf("ingest failed: %s", stderr)
	}
	code, stdout, stderr := run("locate", "--store", paths["store"], "--systems", paths["systems.json"], "--system", "SYS-T")
	if code != 0 {
		t.Fatalf("locate exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "best estimate KP") {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestPlanProducesStagesAndStoresASnapshot(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, stdout, stderr := run("plan", "--store", paths["store"],
		"--systems", paths["systems.json"], "--assets", paths["assets.json"],
		"--weather", paths["weather.jsonl"], "--permits", paths["permits.json"],
		"--evidence", paths["evidence.jsonl"], "--system", "SYS-T", "--candidates")
	if code != 0 {
		t.Fatalf("plan exit %d, stderr %s", code, stderr)
	}
	for _, want := range []string{"mobilize", "transit", "survey", "cut_and_hold", "splice", "test", "final_burial", "demobilize"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("plan output is missing stage %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "feasible:                  yes") {
		t.Fatalf("plan output = %s", stdout)
	}
	entries, err := os.ReadDir(filepath.Join(paths["store"], "snapshots"))
	if err != nil {
		t.Fatalf("read snapshots: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(entries))
	}
}

func TestPlanDryRunDoesNotTouchTheStore(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, _, stderr := run("plan", "--store", paths["store"],
		"--systems", paths["systems.json"], "--assets", paths["assets.json"],
		"--weather", paths["weather.jsonl"], "--permits", paths["permits.json"],
		"--evidence", paths["evidence.jsonl"], "--system", "SYS-T", "--dry-run")
	if code != 0 {
		t.Fatalf("plan exit %d, stderr %s", code, stderr)
	}
	entries, err := os.ReadDir(filepath.Join(paths["store"], "snapshots"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("snapshots = %d, want none after a dry run", len(entries))
	}
	if err == nil {
		return
	}
	if !os.IsNotExist(err) {
		t.Fatalf("read snapshots: %v", err)
	}
}

func TestCampaignSchedulesTheFault(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, stdout, stderr := run("campaign", "--store", paths["store"],
		"--systems", paths["systems.json"], "--assets", paths["assets.json"],
		"--weather", paths["weather.jsonl"], "--permits", paths["permits.json"],
		"--faults", paths["faults.json"], "--evidence", paths["evidence.jsonl"], "--dry-run")
	if code != 0 {
		t.Fatalf("campaign exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "assigned:                  1") {
		t.Fatalf("campaign output = %s", stdout)
	}
}

func TestWindowReportsSelection(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, stdout, stderr := run("window", "--systems", paths["systems.json"],
		"--weather", paths["weather.jsonl"], "--assets", paths["assets.json"],
		"--system", "SYS-T", "--vessel", "CS-ONE", "--hours", "36")
	if code != 0 {
		t.Fatalf("window exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "found:                     yes") {
		t.Fatalf("window output = %s", stdout)
	}
	if !strings.Contains(stdout, "2026-03-10T06:00:00Z") {
		t.Fatalf("window output should start after the rough spell:\n%s", stdout)
	}
}

func TestAssetsRequiresKPWithSystem(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, _, stderr := run("assets", "--assets", paths["assets.json"], "--systems", paths["systems.json"], "--system", "SYS-T")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "--kp is required") {
		t.Fatalf("stderr = %q", stderr)
	}
	code, stdout, stderr := run("assets", "--assets", paths["assets.json"],
		"--systems", paths["systems.json"], "--system", "SYS-T", "--kp", "20")
	if code != 0 {
		t.Fatalf("assets exit %d, stderr %s", code, stderr)
	}
	if !strings.Contains(stdout, "CS-ONE") {
		t.Fatalf("assets output = %s", stdout)
	}
}

func TestVerifyAcceptsAndRejects(t *testing.T) {
	dir, paths := fixtureFiles(t)
	code, stdout, stderr := run("verify", "--store", paths["store"],
		"--systems", paths["systems.json"], "--verification", paths["verification.jsonl"])
	if code != 0 {
		t.Fatalf("verify exit %d, stderr %s, stdout %s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "rejected:                  0") {
		t.Fatalf("verify output = %s", stdout)
	}
	bad := filepath.Join(dir, "bad-verification.jsonl")
	if err := os.WriteFile(bad, []byte(`{"id":"VR-2","system_id":"SYS-T","segment_id":"SEG-1","fault_id":"F-1","measured_at":"2026-03-16T00:00:00Z","measured_loss_db":40.0,"joints_after_repair":2,"repair_kp":20.0,"spare_cable_used_km":8.0,"burial_achieved_m":1.2,"rov_inspection":true,"notes":"high"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, stdout, _ = run("verify", "--store", paths["store"], "--systems", paths["systems.json"], "--verification", bad, "--dry-run")
	if code != 1 {
		t.Fatalf("exit %d, want 1 for a rejected record", code)
	}
	if !strings.Contains(stdout, "measured_loss_above_budget") {
		t.Fatalf("verify output = %s", stdout)
	}
}

func TestOutFileWritesTheDocument(t *testing.T) {
	dir, paths := fixtureFiles(t)
	target := filepath.Join(dir, "nested", "locate.json")
	code, stdout, stderr := run("locate", "--systems", paths["systems.json"],
		"--evidence", paths["evidence.jsonl"], "--format", "json", "--out", target)
	if code != 0 {
		t.Fatalf("locate exit %d, stderr %s", code, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty when --out is used: %q", stdout)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}
	if !strings.Contains(string(data), `"localizations"`) {
		t.Fatalf("out file = %s", data)
	}
}

func TestUnknownFormatIsRejected(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, _, stderr := run("locate", "--systems", paths["systems.json"],
		"--evidence", paths["evidence.jsonl"], "--format", "yaml")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "unknown format") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestUnexpectedPositionalArgumentIsRejected(t *testing.T) {
	_, paths := fixtureFiles(t)
	code, _, stderr := run("locate", "--systems", paths["systems.json"], "extra")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "unexpected argument") {
		t.Fatalf("stderr = %q", stderr)
	}
}
