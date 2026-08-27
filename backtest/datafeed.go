package backtest

import (
	"sort"
	"time"

	"nofx/market"
)

// MemoryFeed is an in-memory Feed implementing the 1-minute outer-join
// iteration over pre-loaded bars (P3.1 data layer). It is the default
// feed for tests and for the market-backed runner (P3.4 loads klines
// via market.GetKlinesRange and feeds them in here).
type MemoryFeed struct {
	symbols   []string
	bars      map[string][]Bar // sorted by Time asc (close time)
	cursor    map[string]int   // next unconsumed bar index per symbol
	lastClose map[string]float64
	funding   map[string][]FundingRate // sorted by Time asc
	oi        map[string][]OIDataPoint // sorted by Time asc
	bucket    time.Time
	end       time.Time
}

// NewMemoryFeed builds a feed over the given bars (close times). Bars
// may arrive unsorted; they are normalized. funding/oi may be nil.
// Iteration starts at the first bar's bucket and ends at end (inclusive).
func NewMemoryFeed(symbols []string, bars map[string][]Bar, end time.Time, funding map[string][]FundingRate, oi map[string][]OIDataPoint) *MemoryFeed {
	f := &MemoryFeed{
		bars:      make(map[string][]Bar, len(bars)),
		cursor:    make(map[string]int, len(bars)),
		lastClose: make(map[string]float64, len(bars)),
		funding:   make(map[string][]FundingRate, len(funding)),
		oi:        make(map[string][]OIDataPoint, len(oi)),
		end:       end,
	}
	for _, sym := range symbols {
		f.symbols = append(f.symbols, sym)
		if bs, ok := bars[sym]; ok {
			f.bars[sym] = SortedBars(bs)
			// drop duplicate timestamps, keep the last one
			dedup := f.bars[sym][:0]
			for _, b := range f.bars[sym] {
				if len(dedup) > 0 && dedup[len(dedup)-1].Time.Equal(b.Time) {
					dedup[len(dedup)-1] = b
					continue
				}
				dedup = append(dedup, b)
			}
			f.bars[sym] = dedup
			if len(dedup) > 0 && dedup[0].Time.Before(f.end) && (f.bucket.IsZero() || dedup[0].Time.Before(f.bucket)) {
				f.bucket = dedup[0].Time
			}
		}
	}
	if f.bucket.IsZero() {
		f.bucket = end
	}
	for sym, fr := range funding {
		sorted := append([]FundingRate(nil), fr...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
		f.funding[sym] = sorted
	}
	for sym, ois := range oi {
		sorted := append([]OIDataPoint(nil), ois...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
		f.oi[sym] = sorted
	}
	return f
}

// NewMemoryFeedFromKlines builds a feed from market.Kline slices
// (Binance-style OpenTime semantics: OpenTime is the bar open time;
// the close time is approximated as OpenTime + bar period). This adapts
// the existing market REST channel (§2 / §7 REST fallback).
func NewMemoryFeedFromKlines(symbols []string, klines map[string][]market.Kline, timeframe time.Duration, end time.Time) *MemoryFeed {
	bars := make(map[string][]Bar, len(klines))
	for sym, ks := range klines {
		bs := make([]Bar, 0, len(ks))
		for _, k := range ks {
			closeTime := time.UnixMilli(k.OpenTime).UTC().Add(timeframe)
			bs = append(bs, Bar{
				Time:   closeTime,
				Open:   k.Open,
				High:   k.High,
				Low:    k.Low,
				Close:  k.Close,
				Volume: k.Volume,
			})
		}
		bars[sym] = bs
	}
	return NewMemoryFeed(symbols, bars, end, nil, nil)
}

// Symbols implements Feed.
func (f *MemoryFeed) Symbols() []string { return f.symbols }

// Next implements Feed: advance one 1-minute bucket; report symbols
// that produced a new bar inside it.
func (f *MemoryFeed) Next() (time.Time, []string, bool) {
	if f.bucket.After(f.end) {
		return time.Time{}, nil, false
	}
	t := f.bucket
	var newBars []string
	for _, sym := range f.symbols {
		bs := f.bars[sym]
		i := f.cursor[sym]
		consumed := false
		for i < len(bs) && !bs[i].Time.After(t) {
			f.lastClose[sym] = bs[i].Close
			i++
			consumed = true
		}
		if consumed {
			f.cursor[sym] = i
			newBars = append(newBars, sym)
		}
	}
	f.bucket = f.bucket.Add(time.Minute)
	return t, newBars, true
}

// LastPrice implements Feed. Returns 0 until the symbol has produced a
// bar (never used by the engine for symbols without data).
func (f *MemoryFeed) LastPrice(symbol string) float64 {
	if p, ok := f.lastClose[symbol]; ok {
		return p
	}
	return 0
}

// LastBar implements Feed.
func (f *MemoryFeed) LastBar(symbol string) (Bar, bool) {
	bs := f.bars[symbol]
	i := f.cursor[symbol]
	if i > 0 && i <= len(bs) {
		return bs[i-1], true
	}
	return Bar{}, false
}

// History implements Feed: all bars with close time <= t (binary
// search; bars are sorted).
func (f *MemoryFeed) History(symbol string, t time.Time) []Bar {
	bs := f.bars[symbol]
	idx := sort.Search(len(bs), func(i int) bool { return bs[i].Time.After(t) })
	return bs[:idx]
}

// FundingRate implements Feed.
func (f *MemoryFeed) FundingRate(symbol string, t time.Time) float64 {
	fr := f.funding[symbol]
	idx := sort.Search(len(fr), func(i int) bool { return fr[i].Time.After(t) })
	if idx == 0 {
		return 0
	}
	return fr[idx-1].Rate
}

// OIData implements Feed.
func (f *MemoryFeed) OIData(symbol string, t time.Time) *OIDataPoint {
	ois := f.oi[symbol]
	idx := sort.Search(len(ois), func(i int) bool { return ois[i].Time.After(t) })
	if idx == 0 {
		return nil
	}
	p := ois[idx-1]
	return &p
}
