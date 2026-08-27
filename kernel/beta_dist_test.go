package kernel

import (
	"math"
	"testing"
)

func TestBetaSample_MeanConverges(t *testing.T) {
	const N = 10000
	const alpha, beta = 2.0, 5.0
	expected := alpha / (alpha + beta) // 2/7 ≈ 0.286

	var sum float64
	for i := 0; i < N; i++ {
		sum += betaSample(alpha, beta)
	}
	mean := sum / N
	if math.Abs(mean-expected) > 0.03 {
		t.Errorf("mean %.4f differs from expected %.4f by >0.03", mean, expected)
	}
}

func TestBetaQuantile_Monotonic(t *testing.T) {
	alpha, beta := 4.0, 2.0
	prev := -1.0
	for _, p := range []float64{0.1, 0.3, 0.5, 0.7, 0.9} {
		q := betaQuantile(p, alpha, beta)
		if q < 0 || q > 1 {
			t.Errorf("quantile(%v) = %v out of [0,1]", p, q)
		}
		if q < prev {
			t.Errorf("quantile not monotonic: %v < %v", q, prev)
		}
		prev = q
	}
}

func TestNormalQuantile_ApproxStandard(t *testing.T) {
	// z(0.5) = 0, z(0.975) ≈ 1.96
	if math.Abs(normalQuantile(0.5)) > 1e-6 {
		t.Errorf("z(0.5) should be ~0")
	}
	if math.Abs(normalQuantile(0.975)-1.96) > 0.01 {
		t.Errorf("z(0.975) should be ~1.96, got %v", normalQuantile(0.975))
	}
}
