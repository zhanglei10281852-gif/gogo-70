package numeric

import (
	"math"
	"testing"
)

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in       float64
		decimals int
		want     float64
	}{
		{1.2345, 3, 1.235},
		{-1.2345, 3, -1.235},
		{2.5, 0, 3},
		{-2.5, 0, -3},
		{0.0004, 3, 0},
		{math.NaN(), 3, 0},
		{math.Inf(1), 3, 0},
	}
	for _, c := range cases {
		if got := Round(c.in, c.decimals); got != c.want {
			t.Fatalf("Round(%v, %d) = %v, want %v", c.in, c.decimals, got, c.want)
		}
	}
	if got := Format(-0.0001, 2); got != "0.00" {
		t.Fatalf("Format = %q, want a normalized zero", got)
	}
}

func TestFormatUsesFixedDecimals(t *testing.T) {
	if got := Format(12.3, 3); got != "12.300" {
		t.Fatalf("Format = %q", got)
	}
	if got := Format(12.3456789, MaxDecimals+5); got != Format(12.3456789, MaxDecimals) {
		t.Fatalf("decimals are not clamped: %q", got)
	}
}

func TestClampAndClampInt(t *testing.T) {
	if got := Clamp(5, 10, 1); got != 5 {
		t.Fatalf("Clamp with swapped bounds = %v", got)
	}
	if got := Clamp(-1, 0, 10); got != 0 {
		t.Fatalf("Clamp low = %v", got)
	}
	if got := ClampInt(11, 0, 10); got != 10 {
		t.Fatalf("ClampInt high = %v", got)
	}
}

func TestDistanceConversions(t *testing.T) {
	km := 100.0
	if got := NmToKm(KmToNm(km)); math.Abs(got-km) > 1e-9 {
		t.Fatalf("round trip = %v", got)
	}
	hours, err := TransitHours(120, 12)
	if err != nil || math.Abs(hours-10) > 1e-9 {
		t.Fatalf("TransitHours = %v, %v", hours, err)
	}
	if _, err := TransitHours(120, 0); err == nil {
		t.Fatal("expected an error for zero speed")
	}
	if _, err := TransitHours(-1, 12); err == nil {
		t.Fatal("expected an error for a negative distance")
	}
}

func TestSortedSumIsOrderIndependent(t *testing.T) {
	values := []float64{1e16, 1, -1e16, 2}
	forward := SortedSum(values)
	reversed := SortedSum([]float64{2, -1e16, 1, 1e16})
	if forward != reversed {
		t.Fatalf("SortedSum is order dependent: %v vs %v", forward, reversed)
	}
	if Sum(nil) != 0 || SortedSum(nil) != 0 {
		t.Fatal("empty sums must be zero")
	}
}

func TestWeightedMeanIgnoresUnusableWeights(t *testing.T) {
	got, ok := WeightedMean([]float64{10, 20}, []float64{3, 1})
	if !ok || math.Abs(got-12.5) > 1e-9 {
		t.Fatalf("WeightedMean = %v, %v", got, ok)
	}
	if _, ok := WeightedMean([]float64{10}, []float64{0}); ok {
		t.Fatal("zero total weight must be reported")
	}
	if _, ok := WeightedMean([]float64{10}, []float64{1, 2}); ok {
		t.Fatal("mismatched lengths must be reported")
	}
	got, ok = WeightedMean([]float64{10, 20}, []float64{math.NaN(), 1})
	if !ok || got != 20 {
		t.Fatalf("WeightedMean with a NaN weight = %v, %v", got, ok)
	}
}

func TestMeanSpreadAndPredicates(t *testing.T) {
	if got := Mean([]float64{1, 2, 3}); got != 2 {
		t.Fatalf("Mean = %v", got)
	}
	if Mean(nil) != 0 {
		t.Fatal("Mean of nothing must be zero")
	}
	if got := Spread([]float64{3, 1, 7}); got != 6 {
		t.Fatalf("Spread = %v", got)
	}
	if Spread(nil) != 0 {
		t.Fatal("Spread of nothing must be zero")
	}
	if !Positive(1) || Positive(0) || Positive(math.NaN()) {
		t.Fatal("Positive is wrong")
	}
	if !NonNegative(0) || NonNegative(-1) || NonNegative(math.Inf(1)) {
		t.Fatal("NonNegative is wrong")
	}
	if !Finite(1) || Finite(math.Inf(-1)) {
		t.Fatal("Finite is wrong")
	}
	if !AlmostEqual(1.0, 1.0001, 1e-3) || AlmostEqual(1.0, 1.1, 1e-3) {
		t.Fatal("AlmostEqual is wrong")
	}
	if CeilInt(1.1) != 2 || CeilInt(2.0) != 2 {
		t.Fatal("CeilInt is wrong")
	}
}
