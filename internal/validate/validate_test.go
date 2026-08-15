package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const systemsDoc = `{
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
      "repeaters": [],
      "branching_units": [],
      "protection_zones": [
        {"id": "PZ-1", "start_kp": 0.0, "end_kp": 35.0, "jurisdiction": "JA",
         "permit_required": true, "anchoring_restricted": true, "risk_weight": 2.0}
      ]
    }
  ]
}`

const evidenceDoc = `{"id":"EV-1","system_id":"SYS-T","fault_id":"F-1","observed_at":"2026-03-10T01:00:00Z","end":"A","method":"otdr","cable_distance_km":21.0,"loss_step_db":3.0,"insulation_resistance_mohm":0.4}
{"id":"EV-2","system_id":"SYS-T","fault_id":"F-1","observed_at":"2026-03-10T01:10:00Z","end":"B","method":"otdr","cable_distance_km":84.0,"loss_step_db":3.1,"insulation_resistance_mohm":0.5}
`

const permitsDoc = `{
  "version": 1,
  "permits": [
    {"id":"PMT-1","system_id":"SYS-T","zone_id":"PZ-1","jurisdiction":"JA",
     "valid_from":"2026-03-01T00:00:00Z","valid_to":"2026-04-01T00:00:00Z",
     "operations":["survey","cut_and_hold","splice","test","burial"],"reference":"JA/1"}
  ]
}`

const faultsDoc = `{
  "version": 1,
  "faults": [
    {"id":"F-1","system_id":"SYS-T","reported_at":"2026-03-10T00:30:00Z","declared_priority":70.0,
     "traffic_tbps":8.0,"single_route":false,"restore_by":"2026-03-25T00:00:00Z","description":"test fault"}
  ]
}`

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func fixture(t *testing.T) (string, Inputs) {
	t.Helper()
	dir := t.TempDir()
	in := Inputs{
		SystemsPath:  writeFile(t, dir, "systems.json", systemsDoc),
		EvidencePath: writeFile(t, dir, "evidence.jsonl", evidenceDoc),
		PermitsPath:  writeFile(t, dir, "permits.json", permitsDoc),
		FaultsPath:   writeFile(t, dir, "faults.json", faultsDoc),
	}
	return dir, in
}

func TestCheckAcceptsAConsistentInputSet(t *testing.T) {
	_, in := fixture(t)
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if !rep.OK {
		t.Fatalf("expected a clean report, errors %+v", rep.Errors)
	}
	if len(rep.Documents) != 4 {
		t.Fatalf("documents = %d, want one per input", len(rep.Documents))
	}
	if len(rep.Checks) == 0 {
		t.Fatal("expected the performed checks to be listed")
	}
	if len(rep.Sources) != 4 {
		t.Fatalf("sources = %v", rep.Sources)
	}
}

func TestCheckDetectsUnknownSystemReference(t *testing.T) {
	dir, in := fixture(t)
	in.EvidencePath = writeFile(t, dir, "bad-evidence.jsonl",
		`{"id":"EV-9","system_id":"SYS-OTHER","fault_id":"F-1","observed_at":"2026-03-10T01:00:00Z","end":"A","method":"otdr","cable_distance_km":10.0}`+"\n")
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if rep.OK {
		t.Fatal("expected the unknown system to be an error")
	}
	if !mentions(rep.Errors, "unknown system") {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func TestCheckDetectsFaultWithoutEvidence(t *testing.T) {
	dir, in := fixture(t)
	in.FaultsPath = writeFile(t, dir, "other-faults.json", strings.Replace(faultsDoc, `"id":"F-1"`, `"id":"F-9"`, 1))
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if rep.OK {
		t.Fatal("expected a fault without evidence to be an error")
	}
	if !mentions(rep.Errors, "no evidence records") {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func TestCheckDetectsPermitForUnknownZone(t *testing.T) {
	dir, in := fixture(t)
	in.PermitsPath = writeFile(t, dir, "bad-permits.json", strings.Replace(permitsDoc, `"zone_id":"PZ-1"`, `"zone_id":"PZ-NOPE"`, 1))
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if rep.OK {
		t.Fatal("expected the unknown zone to be an error")
	}
	if !mentions(rep.Errors, "unknown protection zone") {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func TestCheckWarnsAboutMeasurementsBeyondCableLength(t *testing.T) {
	dir, in := fixture(t)
	in.EvidencePath = writeFile(t, dir, "long-evidence.jsonl",
		`{"id":"EV-1","system_id":"SYS-T","fault_id":"F-1","observed_at":"2026-03-10T01:00:00Z","end":"A","method":"otdr","cable_distance_km":900.0}`+"\n")
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if !mentions(rep.Warnings, "holds") {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
}

func TestCheckWarnsAboutShortWeatherSeries(t *testing.T) {
	dir, in := fixture(t)
	in.WeatherPath = writeFile(t, dir, "weather.jsonl",
		`{"system_id":"SYS-T","hour_start":"2026-03-10T00:00:00Z","wave_height_m":1.0,"wind_speed_kn":10.0,"visibility_km":12.0,"current_kn":0.4,"source":"T"}`+"\n")
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if !rep.OK {
		t.Fatalf("a short series is a warning, not an error: %+v", rep.Errors)
	}
	if !mentions(rep.Warnings, "observation hours") {
		t.Fatalf("warnings = %+v", rep.Warnings)
	}
}

func TestLoadRejectsDuplicateRecordIDs(t *testing.T) {
	dir, in := fixture(t)
	in.EvidencePath = writeFile(t, dir, "dup-evidence.jsonl", evidenceDoc+strings.Split(evidenceDoc, "\n")[0]+"\n")
	if _, err := Load(in); err == nil {
		t.Fatal("expected duplicate ids to fail loading")
	}
}

func TestLoadRejectsUnknownFieldsInEveryDocument(t *testing.T) {
	dir, in := fixture(t)
	in.SystemsPath = writeFile(t, dir, "bad-systems.json", strings.Replace(systemsDoc, `"version": 1,`, `"version": 1, "surprise": true,`, 1))
	if _, err := Load(in); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestLoadRejectsWrongDocumentVersion(t *testing.T) {
	dir, in := fixture(t)
	in.PermitsPath = writeFile(t, dir, "v2-permits.json", strings.Replace(permitsDoc, `"version": 1`, `"version": 2`, 1))
	if _, err := Load(in); err == nil {
		t.Fatal("expected a version mismatch error")
	}
}

func TestLoadVerificationRecords(t *testing.T) {
	dir, in := fixture(t)
	in.VerificationPath = writeFile(t, dir, "verification.jsonl",
		`{"id":"VR-1","system_id":"SYS-T","segment_id":"SEG-1","fault_id":"F-1","measured_at":"2026-03-15T00:00:00Z","measured_loss_db":21.3,"joints_after_repair":2,"repair_kp":20.0,"spare_cable_used_km":8.0,"burial_achieved_m":1.2,"rov_inspection":true,"notes":"ok"}`+"\n")
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded.Verifications) != 1 {
		t.Fatalf("verifications = %d", len(loaded.Verifications))
	}
	rep := Check(in, loaded)
	if !rep.OK {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func TestCheckDetectsVerificationOutsideRoute(t *testing.T) {
	dir, in := fixture(t)
	in.VerificationPath = writeFile(t, dir, "bad-verification.jsonl",
		`{"id":"VR-1","system_id":"SYS-T","segment_id":"SEG-1","fault_id":"F-1","measured_at":"2026-03-15T00:00:00Z","measured_loss_db":21.3,"joints_after_repair":2,"repair_kp":900.0,"spare_cable_used_km":8.0,"burial_achieved_m":1.2,"rov_inspection":true,"notes":"ok"}`+"\n")
	loaded, err := Load(in)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := Check(in, loaded)
	if rep.OK {
		t.Fatal("expected a route bounds error")
	}
	if !mentions(rep.Errors, "outside the route") {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func mentions(problems []Problem, want string) bool {
	for _, p := range problems {
		if strings.Contains(p.Detail, want) {
			return true
		}
	}
	return false
}
