// Package locate turns fault evidence into a route position with an explicit
// uncertainty window. Cable distances measured by a test set are converted to
// route kilometres through the per-segment slack factors, measurements from
// both ends are combined by inverse-variance weighting, and disagreement beyond
// the configured tolerance is reported rather than averaged away.
package locate

import (
	"fmt"
	"math"
	"sort"

	"CableMend/internal/config"
	"CableMend/internal/model"
	"CableMend/internal/numeric"
)

// Flag names produced by localization.
const (
	FlagSingleEnd          = "single_end_evidence"
	FlagEndDisagreement    = "end_disagreement"
	FlagOutlier            = "outlier_evidence"
	FlagClampedMeasurement = "clamped_measurement"
	FlagMixedClass         = "mixed_fault_class"
	FlagUnknownClass       = "unknown_fault_class"
	FlagInsufficient       = "insufficient_evidence"
	FlagWindowClamped      = "window_clamped_to_route"
	FlagWindowWidened      = "window_widened_to_minimum"
	FlagWindowTruncated    = "window_truncated_to_maximum"
	FlagNoDepthData        = "no_depth_profile"
)

// Estimate is the per-record contribution to a localization.
type Estimate struct {
	EvidenceID      string           `json:"evidence_id"`
	End             string           `json:"end"`
	Method          string           `json:"method"`
	Instrument      string           `json:"instrument"`
	ObservedAt      model.UTCTime    `json:"observed_at"`
	MeasuredCableKm float64          `json:"measured_cable_km"`
	RouteKP         float64          `json:"route_kp"`
	RouteKm         float64          `json:"route_km"`
	EffectiveSlack  float64          `json:"effective_slack"`
	UncertaintyKm   float64          `json:"uncertainty_km"`
	Weight          float64          `json:"weight"`
	DeviationKm     float64          `json:"deviation_km"`
	Clamped         bool             `json:"clamped"`
	InlineID        string           `json:"inline_id,omitempty"`
	FaultClass      model.FaultClass `json:"fault_class"`
	LossStepDb      float64          `json:"loss_step_db"`
	Flags           []string         `json:"flags"`
}

// ZoneRef is a protection zone reference carried into downstream planning.
type ZoneRef struct {
	ID                  string  `json:"id"`
	Jurisdiction        string  `json:"jurisdiction"`
	StartKP             float64 `json:"start_kp"`
	EndKP               float64 `json:"end_kp"`
	PermitRequired      bool    `json:"permit_required"`
	AnchoringRestricted bool    `json:"anchoring_restricted"`
	RiskWeight          float64 `json:"risk_weight"`
}

// ClassVote records how many measurements support a fault class.
type ClassVote struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

// Result is a complete fault localization.
type Result struct {
	SystemID          string            `json:"system_id"`
	SystemName        string            `json:"system_name"`
	FaultID           string            `json:"fault_id"`
	EvidenceCount     int               `json:"evidence_count"`
	ObservedFrom      model.UTCTime     `json:"observed_from"`
	ObservedTo        model.UTCTime     `json:"observed_to"`
	BestKP            float64           `json:"best_kp"`
	WindowLowKP       float64           `json:"window_low_kp"`
	WindowHighKP      float64           `json:"window_high_kp"`
	WindowWidthKm     float64           `json:"window_width_km"`
	UncertaintyKm     float64           `json:"uncertainty_km"`
	EndsUsed          []string          `json:"ends_used"`
	EndEstimateAKP    float64           `json:"end_estimate_a_kp"`
	EndEstimateBKP    float64           `json:"end_estimate_b_kp"`
	EndDisagreementKm float64           `json:"end_disagreement_km"`
	ToleranceKm       float64           `json:"tolerance_km"`
	Consistent        bool              `json:"consistent"`
	FaultClass        model.FaultClass  `json:"fault_class"`
	ClassVotes        []ClassVote       `json:"class_votes"`
	LossStepTotalDb   float64           `json:"loss_step_total_db"`
	SegmentID         string            `json:"segment_id"`
	SegmentCableType  string            `json:"segment_cable_type"`
	SegmentSlack      float64           `json:"segment_slack"`
	Zones             []ZoneRef         `json:"zones"`
	Jurisdictions     []string          `json:"jurisdictions"`
	Depth             model.DepthSample `json:"depth"`
	BuriedLengthKm    float64           `json:"buried_length_km"`
	NearestInlineID   string            `json:"nearest_inline_id,omitempty"`
	NearestInlineKind string            `json:"nearest_inline_kind,omitempty"`
	NearestInlineKm   float64           `json:"nearest_inline_distance_km"`
	CableKmFromA      float64           `json:"cable_km_from_a"`
	CableKmFromB      float64           `json:"cable_km_from_b"`
	Flags             []string          `json:"flags"`
	Estimates         []Estimate        `json:"estimates"`
	RouteLengthKm     float64           `json:"route_length_km"`
	CableLengthKm     float64           `json:"cable_length_km"`
	Site              model.Site        `json:"site"`
	Advice            []string          `json:"advice"`
}

// Localize combines evidence for one fault into a single localization result.
func Localize(cfg config.Config, ix *model.SystemIndex, records []model.Evidence, faultID string) (Result, error) {
	if ix == nil {
		return Result{}, fmt.Errorf("localization requires an indexed system")
	}
	if len(records) == 0 {
		return Result{}, fmt.Errorf("system %s: no evidence records to localize", ix.ID())
	}
	sorted := model.SortEvidence(records)
	loc := cfg.Localization
	res := Result{
		SystemID:      ix.ID(),
		SystemName:    ix.System().Name,
		FaultID:       faultID,
		EvidenceCount: len(sorted),
		ToleranceKm:   cfg.Round(loc.DisagreementToleranceKm),
		RouteLengthKm: cfg.Round(ix.RouteLengthKm()),
		CableLengthKm: cfg.Round(ix.CableLengthKm()),
		Consistent:    true,
	}

	flags := newFlagSet()
	var (
		kps        []float64
		weights    []float64
		perEndKPs  = map[model.End][]float64{}
		perEndWts  = map[model.End][]float64{}
		classCount = map[model.FaultClass]int{}
		lossSteps  []float64
		times      []model.UTCTime
	)

	for _, ev := range sorted {
		if ev.SystemID != ix.ID() {
			return Result{}, fmt.Errorf("evidence %s belongs to system %s, not %s", ev.ID, ev.SystemID, ix.ID())
		}
		cableKm, err := ev.MeasuredCableKm()
		if err != nil {
			return Result{}, err
		}
		conv, err := ix.KPFromCableDistance(ev.End, cableKm)
		if err != nil {
			return Result{}, fmt.Errorf("evidence %s: %w", ev.ID, err)
		}
		sigma := uncertaintyFor(cfg, ev, cableKm)
		weight := 1.0 / (sigma * sigma)
		est := Estimate{
			EvidenceID:      ev.ID,
			End:             string(ev.End),
			Method:          string(ev.Method),
			Instrument:      ev.Instrument,
			ObservedAt:      ev.ObservedAt,
			MeasuredCableKm: cfg.Round(cableKm),
			RouteKP:         cfg.Round(conv.KP),
			RouteKm:         cfg.Round(conv.RouteKm),
			EffectiveSlack:  numeric.Round(conv.EffectiveSlack, 6),
			UncertaintyKm:   cfg.Round(sigma),
			Weight:          numeric.Round(weight, 6),
			Clamped:         conv.Clamped,
			InlineID:        conv.InlineID,
			FaultClass:      ev.Classify(loc.ShuntResistanceMohm),
			LossStepDb:      cfg.Round(ev.LossStep()),
		}
		if conv.Clamped {
			est.Flags = append(est.Flags, FlagClampedMeasurement)
			flags.add(FlagClampedMeasurement)
		}
		res.Estimates = append(res.Estimates, est)
		kps = append(kps, conv.KP)
		weights = append(weights, weight)
		perEndKPs[ev.End] = append(perEndKPs[ev.End], conv.KP)
		perEndWts[ev.End] = append(perEndWts[ev.End], weight)
		classCount[est.FaultClass]++
		if ev.LossStepDb != nil {
			lossSteps = append(lossSteps, *ev.LossStepDb)
		}
		times = append(times, ev.ObservedAt)
	}

	best, ok := numeric.WeightedMean(kps, weights)
	if !ok {
		return Result{}, fmt.Errorf("system %s: evidence produced no usable weights", ix.ID())
	}
	sigma := combinedSigma(weights)

	if len(sorted) < loc.MinEvidenceRecords {
		flags.add(FlagInsufficient)
		res.Consistent = false
	}

	ends := make([]string, 0, 2)
	for _, end := range []model.End{model.EndA, model.EndB} {
		if len(perEndKPs[end]) > 0 {
			ends = append(ends, string(end))
		}
	}
	res.EndsUsed = ends
	if len(ends) < 2 {
		flags.add(FlagSingleEnd)
	}
	if meanA, okA := numeric.WeightedMean(perEndKPs[model.EndA], perEndWts[model.EndA]); okA {
		res.EndEstimateAKP = cfg.Round(meanA)
		if meanB, okB := numeric.WeightedMean(perEndKPs[model.EndB], perEndWts[model.EndB]); okB {
			res.EndEstimateBKP = cfg.Round(meanB)
			gap := math.Abs(meanA - meanB)
			res.EndDisagreementKm = cfg.Round(gap)
			if gap > loc.DisagreementToleranceKm {
				flags.add(FlagEndDisagreement)
				res.Consistent = false
			}
		}
	} else if meanB, okB := numeric.WeightedMean(perEndKPs[model.EndB], perEndWts[model.EndB]); okB {
		res.EndEstimateBKP = cfg.Round(meanB)
	}

	maxDeviation := 0.0
	for i := range res.Estimates {
		dev := math.Abs(kps[i] - best)
		res.Estimates[i].DeviationKm = cfg.Round(dev)
		if dev > maxDeviation {
			maxDeviation = dev
		}
		if dev > loc.DisagreementToleranceKm {
			res.Estimates[i].Flags = append(res.Estimates[i].Flags, FlagOutlier)
			flags.add(FlagOutlier)
			res.Consistent = false
		}
		sort.Strings(res.Estimates[i].Flags)
	}

	halfWidth := loc.CoverageFactor * sigma
	if maxDeviation > halfWidth {
		halfWidth = maxDeviation
	}
	width := 2 * halfWidth
	if width < loc.MinWindowKm {
		width = loc.MinWindowKm
		flags.add(FlagWindowWidened)
	}
	if width > loc.MaxWindowKm {
		width = loc.MaxWindowKm
		flags.add(FlagWindowTruncated)
	}
	low, high, clamped := clampWindow(best, width, ix.RouteStartKP(), ix.RouteEndKP())
	if clamped {
		flags.add(FlagWindowClamped)
	}
	best = numeric.Clamp(best, ix.RouteStartKP(), ix.RouteEndKP())

	res.BestKP = cfg.Round(best)
	res.WindowLowKP = cfg.Round(low)
	res.WindowHighKP = cfg.Round(high)
	res.WindowWidthKm = cfg.Round(high - low)
	res.UncertaintyKm = cfg.Round(halfWidth)
	res.LossStepTotalDb = cfg.Round(numeric.SortedSum(lossSteps))
	res.ObservedFrom = model.EarliestTime(times)
	res.ObservedTo = model.LatestTime(times)
	res.Site = model.Site{SystemID: ix.ID(), KP: res.BestKP}

	res.FaultClass, res.ClassVotes = resolveClass(classCount)
	if res.FaultClass == model.ClassUnknown {
		flags.add(FlagUnknownClass)
	}
	if len(res.ClassVotes) > 1 {
		flags.add(FlagMixedClass)
	}

	if seg, ok := ix.SegmentAt(best); ok {
		res.SegmentID = seg.ID
		res.SegmentCableType = seg.CableType
		res.SegmentSlack = numeric.Round(ix.System().SegmentSlack(seg, ix.SlackFallback()), 6)
	}
	zones := ix.ZonesIn(low, high)
	for _, z := range zones {
		res.Zones = append(res.Zones, ZoneRef{
			ID:                  z.ID,
			Jurisdiction:        z.Jurisdiction,
			StartKP:             cfg.Round(z.StartKP),
			EndKP:               cfg.Round(z.EndKP),
			PermitRequired:      z.PermitRequired,
			AnchoringRestricted: z.AnchoringRestricted,
			RiskWeight:          cfg.Round(z.RiskWeight),
		})
	}
	res.Jurisdictions = model.Jurisdictions(zones)
	depth := ix.MaxDepthIn(low, high)
	depth.DepthM = cfg.Round(depth.DepthM)
	depth.BurialDepthM = cfg.Round(depth.BurialDepthM)
	res.Depth = depth
	if !depth.Known {
		flags.add(FlagNoDepthData)
	}
	res.BuriedLengthKm = cfg.Round(ix.BuriedLengthKm(low, high))
	if inline, dist, ok := ix.NearestInline(best); ok {
		res.NearestInlineID = inline.ID
		res.NearestInlineKind = inline.Kind
		res.NearestInlineKm = cfg.Round(dist)
	}
	if fromA, err := ix.CableDistanceFromKP(model.EndA, best); err == nil {
		res.CableKmFromA = cfg.Round(fromA)
	}
	if fromB, err := ix.CableDistanceFromKP(model.EndB, best); err == nil {
		res.CableKmFromB = cfg.Round(fromB)
	}
	res.Flags = flags.sorted()
	res.Advice = advice(res)
	return res, nil
}

// LocalizeAll localizes every fault present in the evidence set for the given
// systems, returning results ordered by system then fault.
func LocalizeAll(cfg config.Config, set *model.SystemSet, records []model.Evidence) ([]Result, error) {
	type key struct {
		system string
		fault  string
	}
	buckets := map[key][]model.Evidence{}
	var order []key
	for _, ev := range records {
		k := key{system: ev.SystemID, fault: ev.FaultID}
		if _, seen := buckets[k]; !seen {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], ev)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].system != order[j].system {
			return order[i].system < order[j].system
		}
		return order[i].fault < order[j].fault
	})
	out := make([]Result, 0, len(order))
	for _, k := range order {
		ix, err := set.MustGet(k.system)
		if err != nil {
			return nil, err
		}
		res, err := Localize(cfg, ix, buckets[k], k.fault)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// uncertaintyFor returns the one-sigma positional uncertainty of a measurement
// in kilometres of route.
func uncertaintyFor(cfg config.Config, ev model.Evidence, cableKm float64) float64 {
	if ev.UncertaintyKm != nil && *ev.UncertaintyKm > 0 {
		return *ev.UncertaintyKm
	}
	base := cfg.Localization.OTDRUncertaintyKm
	switch ev.Method {
	case model.MethodResistance:
		base = cfg.Localization.ResistanceUncertaintyKm
	case model.MethodVoltage:
		base = cfg.Localization.VoltageUncertaintyKm
	}
	sigma := base + cfg.Localization.RelativeUncertainty*cableKm
	if sigma <= 0 {
		sigma = 1e-3
	}
	return sigma
}

// combinedSigma returns the one-sigma uncertainty of the inverse-variance
// weighted mean.
func combinedSigma(weights []float64) float64 {
	total := numeric.SortedSum(weights)
	if total <= 0 {
		return 0
	}
	return math.Sqrt(1.0 / total)
}

// clampWindow centres a window of the requested width on best and slides it
// inside the route bounds, shrinking only when the route is shorter than the
// window.
func clampWindow(best, width, routeStart, routeEnd float64) (float64, float64, bool) {
	half := width / 2
	low := best - half
	high := best + half
	clamped := false
	if low < routeStart {
		shift := routeStart - low
		low = routeStart
		high += shift
		clamped = true
	}
	if high > routeEnd {
		shift := high - routeEnd
		high = routeEnd
		low -= shift
		if low < routeStart {
			low = routeStart
		}
		clamped = true
	}
	return low, high, clamped
}

// resolveClass picks the fault class with the most support, breaking ties by
// class name so the outcome never depends on map ordering.
func resolveClass(counts map[model.FaultClass]int) (model.FaultClass, []ClassVote) {
	votes := make([]ClassVote, 0, len(counts))
	for cls, n := range counts {
		votes = append(votes, ClassVote{Class: string(cls), Count: n})
	}
	sort.Slice(votes, func(i, j int) bool {
		if votes[i].Count != votes[j].Count {
			return votes[i].Count > votes[j].Count
		}
		return votes[i].Class < votes[j].Class
	})
	if len(votes) == 0 {
		return model.ClassUnknown, votes
	}
	winner := model.FaultClass(votes[0].Class)
	if len(votes) > 1 && votes[0].Count == votes[1].Count && winner == model.ClassUnknown {
		winner = model.FaultClass(votes[1].Class)
	}
	return winner, votes
}

// advice turns the localization flags into short operator notes.
func advice(res Result) []string {
	var out []string
	for _, flag := range res.Flags {
		switch flag {
		case FlagSingleEnd:
			out = append(out, "only one end reported evidence; request a measurement from the opposite end")
		case FlagEndDisagreement:
			out = append(out, "end-to-end estimates disagree beyond tolerance; re-test before committing a vessel")
		case FlagOutlier:
			out = append(out, "at least one measurement is an outlier; review instrument calibration")
		case FlagClampedMeasurement:
			out = append(out, "a measured distance exceeded the installed cable length and was clamped")
		case FlagMixedClass:
			out = append(out, "fault class is mixed across measurements; treat the electrical picture as unconfirmed")
		case FlagNoDepthData:
			out = append(out, "no burial profile covers the window; vessel depth capability cannot be checked")
		case FlagWindowTruncated:
			out = append(out, "uncertainty exceeded the configured maximum window and was truncated")
		}
	}
	if len(out) == 0 {
		out = append(out, "evidence is internally consistent")
	}
	sort.Strings(out)
	return out
}

// flagSet collects unique flags.
type flagSet struct {
	seen map[string]bool
}

func newFlagSet() *flagSet { return &flagSet{seen: map[string]bool{}} }

func (f *flagSet) add(flag string) { f.seen[flag] = true }

func (f *flagSet) sorted() []string {
	out := make([]string, 0, len(f.seen))
	for k := range f.seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
