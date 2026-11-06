package binance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

// binanceIncomeLimit is the venue cap for one income-history page.
const binanceIncomeLimit = 1000

// binanceIncomePrefix tags every income row with the venue it came from, so a
// TranID from one account can never collide with another account's ledger.
const binanceIncomePrefix = "binance:income:"

// ManagedHistory replays settled money movements. Income history is the only
// venue record that already carries a unique TranID, so no sequence is
// invented to order it: the event ID is the venue's own identifier.
func (b *FuturesTrader) ManagedHistory() execution.HistoryReader {
	return binanceHistory{trader: b}
}

type binanceHistory struct{ trader *FuturesTrader }

// History pages the income history forward from fromTime. A short page means
// the window is finished; a full page returns the next start time as the
// cursor. Fixtures recorded 2026-11-06 from the USDⓈ-M futures docs
// (/fapi/v1/income, fields tranId/incomeType/income/asset/time).
func (h binanceHistory) History(ctx context.Context, fromTime time.Time, pageCursor string) ([]execution.AccountEvent, string, bool, error) {
	start := fromTime
	if pageCursor != "" {
		ms, err := strconv.ParseInt(pageCursor, 10, 64)
		if err != nil {
			return nil, "", false, errors.New("managed history cursor is not a timestamp")
		}
		start = time.UnixMilli(ms).UTC()
	}
	rows, err := h.trader.client.NewGetIncomeHistoryService().
		StartTime(start.UnixMilli()).
		Limit(binanceIncomeLimit).
		Do(ctx)
	if err != nil {
		return nil, "", false, fmt.Errorf("could not read the income history: %w", err)
	}
	events := make([]execution.AccountEvent, 0, len(rows))
	var last int64
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.TranID == 0 {
			// A row without the venue's own id cannot be deduped, and dropping it
			// would silently widen the day's loss budget. Fail the window instead:
			// the cursor stays put and the next attempt re-reads it.
			return nil, "", false, errors.New("income history row carries no tranId")
		}
		if row.Time > last {
			last = row.Time
		}
		events = append(events, execution.AccountEvent{
			ID:         binanceIncomePrefix + strconv.FormatInt(row.TranID, 10),
			Kind:       binanceIncomeKind(row.IncomeType),
			Symbol:     row.Symbol,
			Amount:     execution.Decimal(row.Income),
			Currency:   row.Asset,
			OccurredAt: time.UnixMilli(row.Time).UTC(),
		})
	}
	if len(rows) < binanceIncomeLimit {
		// The window end is the last row the venue returned, not the wall clock:
		// a row that appears later with an older timestamp must still fall inside
		// the overlapping replay. An empty page has nothing to miss, so it ends now.
		if last == 0 {
			return events, time.Now().UTC().Format(time.RFC3339), true, nil
		}
		return events, time.UnixMilli(last).UTC().Format(time.RFC3339), true, nil
	}
	// Advance to the boundary millisecond itself, not past it: funding settles at
	// one timestamp across many symbols, so last+1 would skip the rest of that
	// millisecond. Dedup is by tranId, so re-reading a page costs nothing.
	return events, strconv.FormatInt(last, 10), false, nil
}

// binanceIncomeKind maps the venue's income type onto the ledger vocabulary.
// Anything else is a gap: an unrecognised movement still changes equity, and
// pretending it did not would silently widen the day's loss budget.
func binanceIncomeKind(incomeType string) string {
	switch incomeType {
	case "FUNDING_FEE":
		return risk.KindFunding
	case "COMMISSION":
		return risk.KindFee
	case "REALIZED_PNL", "TRANSFER", "INTERNAL_TRANSFER", "INSURANCE_CLEAR", "REFERRAL_KICKBACK":
		return risk.KindCashflow
	}
	return risk.KindFundingGap
}
