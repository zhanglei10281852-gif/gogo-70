package permits

import (
	"testing"

	"CableMend/internal/model"
)

func zones() []model.ProtectionZone {
	return []model.ProtectionZone{
		{ID: "PZ-1", StartKP: 0, EndKP: 20, Jurisdiction: "JA", PermitRequired: true},
		{ID: "PZ-2", StartKP: 20, EndKP: 40, Jurisdiction: "JB", PermitRequired: false},
	}
}

func permit(id, zone string, from, to string, ops ...model.Operation) model.Permit {
	return model.Permit{
		ID:           id,
		SystemID:     "SYS-T",
		ZoneID:       zone,
		Jurisdiction: "JA",
		ValidFrom:    model.MustParseUTC(from),
		ValidTo:      model.MustParseUTC(to),
		Operations:   ops,
	}
}

func window(from, to string) model.Interval {
	return model.Interval{Start: model.MustParseUTC(from), End: model.MustParseUTC(to)}
}

func allOps() []model.Operation { return model.AllOperations() }

func TestAssessAcceptsCoveringPermit(t *testing.T) {
	all := []model.Permit{permit("PMT-1", "PZ-1", "2026-03-01T00:00:00Z", "2026-04-01T00:00:00Z", allOps()...)}
	got := Assess("SYS-T", zones(), all, window("2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"), allOps())
	if !got.Satisfied {
		t.Fatalf("expected the window to be authorized: %+v", got.Blocking)
	}
	if len(got.UsedPermitIDs) != 1 || got.UsedPermitIDs[0] != "PMT-1" {
		t.Fatalf("used permits = %v", got.UsedPermitIDs)
	}
	if len(got.Requirements) != 2 {
		t.Fatalf("requirements = %d, want one per zone", len(got.Requirements))
	}
	for _, req := range got.Requirements {
		if req.ZoneID == "PZ-2" && !req.Satisfied {
			t.Fatal("a zone without a permit requirement must be satisfied")
		}
	}
}

func TestAssessRejectsWindowOutsideValidity(t *testing.T) {
	all := []model.Permit{permit("PMT-1", "PZ-1", "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z", allOps()...)}
	got := Assess("SYS-T", zones(), all, window("2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"), allOps())
	if got.Satisfied {
		t.Fatal("expected the assessment to fail")
	}
	if len(got.Blocking) == 0 {
		t.Fatal("expected a blocking reason")
	}
}

func TestAssessRejectsMissingOperation(t *testing.T) {
	all := []model.Permit{permit("PMT-1", "PZ-1", "2026-03-01T00:00:00Z", "2026-04-01T00:00:00Z",
		model.OpSurvey, model.OpCutAndHold, model.OpSplice, model.OpTest)}
	got := Assess("SYS-T", zones(), all, window("2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"), allOps())
	if got.Satisfied {
		t.Fatal("expected the burial operation to block the assessment")
	}
	found := false
	for _, req := range got.Requirements {
		for _, cand := range req.Candidates {
			for _, missing := range cand.MissingOperations {
				if missing == string(model.OpBurial) {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("expected the missing operation to be reported")
	}
}

func TestAssessReportsMissingPermitEntirely(t *testing.T) {
	got := Assess("SYS-T", zones(), nil, window("2026-03-10T00:00:00Z", "2026-03-11T00:00:00Z"), allOps())
	if got.Satisfied {
		t.Fatal("expected the assessment to fail without permits")
	}
	if got.Requirements[0].Note == "" {
		t.Fatal("expected an explanatory note")
	}
}

func TestCandidateStartsAreSortedAndFiltered(t *testing.T) {
	all := []model.Permit{
		permit("PMT-LATE", "PZ-1", "2026-03-20T00:00:00Z", "2026-04-01T00:00:00Z", allOps()...),
		permit("PMT-EARLY", "PZ-1", "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z", allOps()...),
	}
	notBefore := model.MustParseUTC("2026-03-10T00:00:00Z")
	starts := CandidateStarts("SYS-T", zones(), all, notBefore)
	if len(starts) != 2 {
		t.Fatalf("starts = %v, want the earliest start and the later permit boundary", starts)
	}
	if !starts[0].Equal(notBefore) {
		t.Fatalf("first start = %s, want the not-before instant", starts[0])
	}
	if starts[1].String() != "2026-03-20T00:00:00Z" {
		t.Fatalf("second start = %s", starts[1])
	}
}

func TestLatestExpiryAndCovers(t *testing.T) {
	all := []model.Permit{
		permit("PMT-1", "PZ-1", "2026-03-01T00:00:00Z", "2026-03-15T00:00:00Z", allOps()...),
		permit("PMT-2", "PZ-1", "2026-03-16T00:00:00Z", "2026-04-20T00:00:00Z", allOps()...),
	}
	if got := LatestExpiry("SYS-T", zones(), all); got.String() != "2026-04-20T00:00:00Z" {
		t.Fatalf("latest expiry = %s", got)
	}
	if !Covers("SYS-T", zones(), all, window("2026-03-17T00:00:00Z", "2026-03-18T00:00:00Z"), allOps()) {
		t.Fatal("expected the second permit to cover the window")
	}
	if Covers("SYS-T", zones(), all, window("2026-03-15T06:00:00Z", "2026-03-15T12:00:00Z"), allOps()) {
		t.Fatal("expected the gap between permits to block the window")
	}
}

func TestRequiredZonesFiltersAndSorts(t *testing.T) {
	got := RequiredZones([]model.ProtectionZone{
		{ID: "PZ-9", PermitRequired: true},
		{ID: "PZ-0", PermitRequired: false},
		{ID: "PZ-3", PermitRequired: true},
	})
	if len(got) != 2 || got[0].ID != "PZ-3" || got[1].ID != "PZ-9" {
		t.Fatalf("required zones = %v", model.ZoneIDs(got))
	}
}
