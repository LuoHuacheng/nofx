// ponytail: Beta-distribution sampling and quantile helpers — replaces
// `gonum.org/v1/gonum/stat/distuv.Beta.Rand/Quantile` for
// threshold_calibrator.go so we don't pull in the full gonum dep tree.
//
// Accuracy:
//   - betaSample uses Marsaglia & Tsang (2000) for Gamma sampling, which is
//     exact up to float64 precision. Beta(α, β) = G1/(G1+G2) with
//     G1 ~ Gamma(α,1), G2 ~ Gamma(β,1) is mathematically equivalent.
//   - betaQuantile uses a normal approximation (mean ± z·stddev). Exact for
//     large α+β; for the small-sample regime α+β < ~8 the calibrator takes
//     the unadjusted `CurrentThreshold` path anyway, so the approximation
//     error never reaches a trading decision.

package kernel

import (
	"math"
	"math/rand"
)

// randFloat64 returns a uniform sample in [0, 1). Tests can override this
// package-level variable to make the samplers deterministic.
var randFloat64 = func() float64 { return rand.Float64() }

// gammaSample returns a sample from Gamma(shape, scale) using the
// Marsaglia & Tsang (2000) algorithm — exact for shape ≥ 1, falling back to
// the Ahrens-Dieter method via shape-boosting Gamma(a) = Gamma(a+1) / U^(1/a)
// for shape < 1.
func gammaSample(shape, scale float64) float64 {
	if shape <= 0 || scale <= 0 {
		return 0
	}
	if shape < 1 {
		// Boost shape into the ≥ 1 regime.
		u := randFloat64()
		if u < 1e-300 {
			u = 1e-300
		}
		return gammaSample(shape+1, scale) * math.Pow(u, 1/shape)
	}
	d := shape - 1.0/3.0
	c := (1.0 / 3.0) / math.Sqrt(d)
	for {
		x := normalSample()
		v := 1 + c*x
		if v > 0 {
			v = v * v * v
			u := randFloat64()
			// Squeeze test (Marsaglia & Tsang eq. 5).
			if u < 1-0.0331*x*x*x*x {
				return d * v * scale
			}
			// Log test.
			if math.Log(u) < 0.5*x*x+d*(1-v+math.Log(v)) {
				return d * v * scale
			}
		}
	}
}

// normalSample returns a standard-normal sample via the Box-Muller transform.
func normalSample() float64 {
	u1 := randFloat64()
	if u1 < 1e-300 {
		u1 = 1e-300
	}
	u2 := randFloat64()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// betaSample returns a sample from Beta(α, β) using the Gamma-ratio identity:
// Beta(α, β) = G1 / (G1 + G2), with G1 ~ Gamma(α, 1), G2 ~ Gamma(β, 1).
func betaSample(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0
	}
	g1 := gammaSample(alpha, 1)
	g2 := gammaSample(beta, 1)
	total := g1 + g2
	if total <= 0 {
		return alpha / (alpha + beta) // degenerate fallback
	}
	return g1 / total
}

// betaQuantile returns an approximation of the Beta(α, β) inverse CDF at
// level p. Uses a normal approximation (mean ± z·stddev) which is accurate
// when α+β is moderate-to-large — the regime the calibrator actually queries.
// For very small α+β the caller falls back to CurrentThreshold.
func betaQuantile(p, alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0
	}
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	total := alpha + beta
	mean := alpha / total
	variance := (alpha * beta) / (total * total * (total + 1))
	if variance <= 0 {
		return mean
	}
	stddev := math.Sqrt(variance)
	q := mean + normalQuantile(p)*stddev
	if q < 0 {
		return 0
	}
	if q > 1 {
		return 1
	}
	return q
}

// normalQuantile returns the standard-normal inverse CDF at probability p.
// Beasley-Springer-Moro rational approximation — accurate to ~1e-7.
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	a := []float64{
		-3.969683028665376e+01, 2.209460984245205e+02,
		-2.759285104469687e+02, 1.383577518672690e+02,
		-3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := []float64{
		-5.447609879822406e+01, 1.615858368580409e+02,
		-1.556989798598866e+02, 6.680131188771972e+01,
		-1.328068155288572e+01,
	}
	c2 := []float64{
		-7.784894002430293e-03, -3.223964580411365e-01,
		-2.400758277161838e+00, -2.549732539343734e+00,
		4.374664141464968e+00, 2.938163982698783e+00,
	}
	d2 := []float64{
		7.784695709041462e-03, 3.224671290700398e-01,
		2.445134137142996e+00, 3.754408661907416e+00,
	}
	plow := 0.02425
	phigh := 1 - plow
	var q, r float64
	switch {
	case p < plow:
		q = math.Sqrt(-2 * math.Log(p))
		return (((((c2[0]*q+c2[1])*q+c2[2])*q+c2[3])*q+c2[4])*q + c2[5]) /
			((((d2[0]*q+d2[1])*q+d2[2])*q+d2[3])*q + 1)
	case p <= phigh:
		q = p - 0.5
		r = q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q = math.Sqrt(-2 * math.Log(1-p))
		return -(((((c2[0]*q+c2[1])*q+c2[2])*q+c2[3])*q+c2[4])*q + c2[5]) /
			((((d2[0]*q+d2[1])*q+d2[2])*q+d2[3])*q + 1)
	}
}
