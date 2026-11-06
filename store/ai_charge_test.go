package store

import (
	"math"
	"testing"
)

func TestGPT6Pricing(t *testing.T) {
	if got, want := GetModelPrice("gpt-6"), 0.24; got != want {
		t.Fatalf("GetModelPrice(gpt-6) = %v, want %v", got, want)
	}

	tests := []struct {
		name             string
		promptTokens     int
		completionTokens int
		want             float64
	}{
		{
			name:             "standard context",
			promptTokens:     100000,
			completionTokens: 10000,
			want:             (100000*12.5 + 10000*50.0) / 1e6 * uptoSafetyMargin,
		},
		{
			name:             "long context",
			promptTokens:     272001,
			completionTokens: 10000,
			want:             (272001*25.0 + 10000*75.0) / 1e6 * uptoSafetyMargin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ComputeUsageCost("gpt-6", tt.promptTokens, tt.completionTokens)
			if !ok {
				t.Fatal("ComputeUsageCost(gpt-6) reported unsupported model")
			}
			tt.want = math.Ceil((tt.want+uptoServiceFeeUSD)*1e6) / 1e6
			if math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("ComputeUsageCost(gpt-6) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLatestModelEstimatesIncludeFeeAndLongContext(t *testing.T) {
	for _, tc := range []struct {
		model   string
		in, out float64
		long    bool
	}{
		{"gpt-6.1-sol", 2.5, 10, true},
		{"gpt-6-sol", 2.5, 10, true},
		{"gpt-6-luna", .125, .5, true},
		{"deepseek-flash", .3, 1.2, false},
		{"glm-5", 1, 3.2, false},
	} {
		for _, tokens := range []int{0, 272000, 272001} {
			in, out := tc.in, tc.out
			if tc.long && tokens > 272000 {
				in *= 2
				out *= 1.5
			}
			want := math.Ceil((math.Max((float64(tokens)*in+256*out)*1.15/1e6, .0001)+.0012)*1e6) / 1e6
			got, ok := ComputeUsageCost("claw402/"+tc.model, tokens, 256)
			if !ok || math.Abs(got-want) > .00000101 {
				t.Fatalf("%s: got %v want %v", tc.model, got, want)
			}
		}
	}
	if _, ok := ComputeUsageCost("unpriced-new-model", 100, 100); ok {
		t.Fatal("unknown model priced")
	}
	if _, ok := ComputeUsageCost("gpt-6-luna", -1, 100); ok {
		t.Fatal("negative usage accepted")
	}
}
