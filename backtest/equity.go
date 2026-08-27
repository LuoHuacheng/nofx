package backtest

import "time"

// EquityPoint is one equity-curve sample.
type EquityPoint struct {
	Time    time.Time
	Balance float64
	Equity  float64 // balance + unrealized PnL
}

// EquityCurve samples the account equity over time. Sampling happens
// every EquitySampleInterval time buckets, plus on every trade event
// (forced sample). The point count is capped at MaxEquityPoints.
type EquityCurve struct {
	points        []EquityPoint
	bucketIndex   int64
	forceNext     bool
	lastSampleIdx int64
	lastSample    EquityPoint
}

// NewEquityCurve starts an empty curve with the initial sample at t0.
func NewEquityCurve(t0 time.Time, initialBalance float64) *EquityCurve {
	ec := &EquityCurve{lastSampleIdx: -1}
	ec.points = append(ec.points, EquityPoint{Time: t0, Balance: initialBalance, Equity: initialBalance})
	ec.lastSample = ec.points[0]
	return ec
}

// Force schedules an unconditional sample on the next Sample call.
func (e *EquityCurve) Force() { e.forceNext = true }

// Sample records a point if the sampling rule says so:
//   - forced (trade event or Force call), or
//   - every EquitySampleInterval buckets.
//
// The curve is monotonic in time: a sample at an earlier time than the
// last recorded one is dropped. After MaxEquityPoints is reached the
// curve stops growing (the persisted store also enforces the cap).
func (e *EquityCurve) Sample(t time.Time, balance, equity float64) {
	e.bucketIndex++
	if !e.forceNext && e.bucketIndex%e.EquitySampleInterval() != 0 {
		return
	}
	if t.Before(e.lastSample.Time) {
		e.forceNext = false
		return
	}
	e.forceNext = false
	if len(e.points) >= MaxEquityPoints {
		return
	}
	p := EquityPoint{Time: t, Balance: balance, Equity: equity}
	e.points = append(e.points, p)
	e.lastSample = p
}

// EquitySampleInterval returns the bucket interval constant.
func (e *EquityCurve) EquitySampleInterval() int64 { return EquitySampleInterval }

// Points returns the sampled points.
func (e *EquityCurve) Points() []EquityPoint {
	out := make([]EquityPoint, len(e.points))
	copy(out, e.points)
	return out
}

// Last returns the most recent point.
func (e *EquityCurve) Last() EquityPoint { return e.points[len(e.points)-1] }

// Size returns the number of recorded points.
func (e *EquityCurve) Size() int { return len(e.points) }
