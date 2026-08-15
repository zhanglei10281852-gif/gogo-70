// Package numeric holds the deterministic arithmetic helpers used across
// CableMend. Every value that reaches an output document passes through one of
// these functions so that formatting and rounding never depend on platform or
// iteration order.
package numeric

import (
	"fmt"
	"math"
	"sort"
	"strconv"
)

// NauticalMilesPerKilometre is the fixed conversion factor used for transit
// distance calculations.
const NauticalMilesPerKilometre = 0.5399568034557235

// MaxDecimals bounds the configurable output precision.
const MaxDecimals = 9

// Round rounds v to the given number of decimals using half-away-from-zero.
func Round(v float64, decimals int) float64 {
	if decimals < 0 {
		decimals = 0
	}
	if decimals > MaxDecimals {
		decimals = MaxDecimals
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	scale := math.Pow(10, float64(decimals))
	scaled := v * scale
	rounded := math.Floor(math.Abs(scaled) + 0.5)
	if v < 0 {
		rounded = -rounded
	}
	out := rounded / scale
	if out == 0 {
		// Normalize negative zero so JSON output is stable.
		return 0
	}
	return out
}

// Format renders v with a fixed number of decimals, clamped to the supported
// precision range so that callers cannot widen the output beyond MaxDecimals.
func Format(v float64, decimals int) string {
	decimals = ClampInt(decimals, 0, MaxDecimals)
	return strconv.FormatFloat(Round(v, decimals), 'f', decimals, 64)
}

// Clamp constrains v to [lo, hi]. When lo exceeds hi the bounds are swapped.
func Clamp(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ClampInt constrains an integer to [lo, hi].
func ClampInt(v, lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// KmToNm converts kilometres to nautical miles.
func KmToNm(km float64) float64 { return km * NauticalMilesPerKilometre }

// NmToKm converts nautical miles to kilometres.
func NmToKm(nm float64) float64 { return nm / NauticalMilesPerKilometre }

// TransitHours converts a distance in nautical miles into hours at the given
// speed in knots.
func TransitHours(distanceNm, speedKn float64) (float64, error) {
	if speedKn <= 0 {
		return 0, fmt.Errorf("transit speed must be positive, got %g", speedKn)
	}
	if distanceNm < 0 {
		return 0, fmt.Errorf("transit distance must not be negative, got %g", distanceNm)
	}
	return distanceNm / speedKn, nil
}

// Sum adds the values in a stable order (input order) so that floating point
// accumulation is reproducible.
func Sum(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}

// SortedSum adds the values from smallest to largest magnitude, which keeps the
// result independent of the caller's iteration order.
func SortedSum(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := make([]float64, len(values))
	copy(cp, values)
	sort.Float64s(cp)
	return Sum(cp)
}

// Mean returns the arithmetic mean, or zero for an empty slice.
func Mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return SortedSum(values) / float64(len(values))
}

// WeightedMean computes sum(w*v)/sum(w). It reports false when the total weight
// is not positive.
func WeightedMean(values, weights []float64) (float64, bool) {
	if len(values) == 0 || len(values) != len(weights) {
		return 0, false
	}
	num := make([]float64, 0, len(values))
	den := make([]float64, 0, len(weights))
	for i, v := range values {
		w := weights[i]
		if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			continue
		}
		num = append(num, v*w)
		den = append(den, w)
	}
	total := SortedSum(den)
	if total <= 0 {
		return 0, false
	}
	return SortedSum(num) / total, true
}

// Spread returns max-min for the given values, or zero when empty.
func Spread(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return hi - lo
}

// AlmostEqual compares two floats with an absolute tolerance.
func AlmostEqual(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// Positive reports whether v is a usable positive finite number.
func Positive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// NonNegative reports whether v is a usable finite number of at least zero.
func NonNegative(v float64) bool {
	return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// Finite reports whether v is a real number.
func Finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// CeilInt returns the smallest integer greater than or equal to v.
func CeilInt(v float64) int { return int(math.Ceil(v)) }
