package okx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"nofx/trader/execution"
	"nofx/trading/risk"
)

const (
	// okxBillsPath is the account bill endpoint. Fixtures recorded 2026-11-06
	// from the OKX v5 docs: billId is the venue's own unique identifier and the
	// `after` cursor walks towards older records.
	okxBillsPath = "/api/v5/account/bills"
	// okxBillsPage is the venue cap for one page (docs allow 100).
	okxBillsPage = 100
	// okxBillPrefix tags bills so an OKX id can never collide with another venue.
	okxBillPrefix = "okx:bill:"
)

// okxBill is the subset of a bill row the ledger attributes. Every other field
// is ignored rather than defaulted: an unknown shape must not become a zero.
type okxBill struct {
	BillID  string `json:"billId"`
	Type    string `json:"type"`
	SubType string `json:"subType"`
	TS      string `json:"ts"`
	Bal     string `json:"bal"`
	PNL     string `json:"pnl"`
	Fee     string `json:"fee"`
	Ccy     string `json:"ccy"`
	InstID  string `json:"instId"`
	OrdID   string `json:"ordId"`
}

// ManagedHistory replays the account bills. Bills carry a native billId, so
// ordering and dedup use the venue's own identifiers instead of a synthetic
// sequence.
func (t *OKXTrader) ManagedHistory() execution.HistoryReader {
	return okxHistory{trader: t}
}

type okxHistory struct{ trader *OKXTrader }

// History pages bills forward from fromTime. The venue returns newest first and
// pages with `after`, so a page is accumulated and reversed before returning.
func (h okxHistory) History(ctx context.Context, fromTime time.Time, pageCursor string) ([]execution.AccountEvent, string, bool, error) {
	limit := okxBillsPage
	query := fmt.Sprintf("%s?instType=SWAP&limit=%d&begin=%d", okxBillsPath, limit, fromTime.UnixMilli())
	if pageCursor != "" {
		query += "&after=" + pageCursor
	}
	data, err := h.trader.doRequestContext(ctx, "GET", query, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("could not read the account bills: %w", err)
	}
	var bills []okxBill
	if err := json.Unmarshal(data, &bills); err != nil {
		return nil, "", false, fmt.Errorf("could not parse the account bills: %w", err)
	}
	events := make([]execution.AccountEvent, 0, len(bills))
	for _, bill := range bills {
		event, ok := okxBillEvent(bill)
		if !ok {
			continue
		}
		events = append(events, event)
	}
	if len(bills) < limit {
		return events, time.Now().UTC().Format(time.RFC3339), true, nil
	}
	return events, bills[len(bills)-1].BillID, false, nil
}

// okxBillEvent converts one bill row. A row without a billId cannot be deduped,
// so it is dropped rather than replayed twice on every restart.
func okxBillEvent(bill okxBill) (execution.AccountEvent, bool) {
	if bill.BillID == "" {
		return execution.AccountEvent{}, false
	}
	ts, err := strconv.ParseInt(bill.TS, 10, 64)
	if err != nil {
		return execution.AccountEvent{}, false
	}
	amount, err := okxBillAmount(bill)
	if err != nil {
		return execution.AccountEvent{}, false
	}
	return execution.AccountEvent{
		ID:         okxBillPrefix + bill.BillID,
		Kind:       okxBillKind(bill.Type),
		Symbol:     bill.InstID,
		Amount:     amount,
		Currency:   bill.Ccy,
		OccurredAt: time.UnixMilli(ts).UTC(),
	}, true
}

// okxBillAmount prefers the balance change the venue already computed; a bill
// that only reports pnl or fee still carries a real money movement.
func okxBillAmount(bill okxBill) (execution.Decimal, error) {
	for _, candidate := range []string{bill.Bal, bill.PNL, bill.Fee} {
		if candidate == "" {
			continue
		}
		if _, err := execution.Number(candidate); err != nil {
			return "", errors.New("account bill has an unparsable amount")
		}
		return execution.Decimal(candidate), nil
	}
	return "", errors.New("account bill has no amount")
}

// okxBillKind maps the venue bill type onto the ledger vocabulary. An unknown
// type becomes a gap so the day stays incomplete instead of silently widening
// the loss budget.
func okxBillKind(billType string) string {
	switch billType {
	case "8":
		return risk.KindFunding
	case "9":
		return risk.KindFee
	case "1", "2", "5", "13", "14":
		return risk.KindCashflow
	}
	return risk.KindFundingGap
}
