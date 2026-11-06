package payment

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"nofx/internal/permit2"
	"nofx/wallet"
)

type uptoEnvelope struct {
	Accepted X402AcceptOption `json:"accepted"`
	Payload  permit2.Payload  `json:"payload"`
}
type settlementReport struct {
	Type        string `json:"type,omitempty"`
	Success     bool   `json:"success"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
	Amount      string `json:"amount"`
}

func verifyUsageReport(ctx context.Context, envelope uptoEnvelope, report settlementReport, endpoint string) error {
	amount, err := permit2.Uint256(report.Amount)
	if err != nil {
		return fmt.Errorf("missing or invalid settled amount")
	}
	cap, err := permit2.Uint256(envelope.Payload.Authorization.Permitted.Amount)
	if err != nil || amount.Cmp(cap) > 0 || !report.Success || report.Network != BaseNetwork ||
		!strings.EqualFold(report.Payer, envelope.Payload.Authorization.From) {
		return fmt.Errorf("settlement report does not match authorization")
	}
	if amount.Sign() == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return permit2.VerifySettlement(ctx, endpoint, report.Transaction, envelope.Payload.Authorization, amount)
}

func receiptRPC() string {
	if s := os.Getenv("NOFX_X402_RECEIPT_RPC_URL"); s != "" {
		return s
	}
	return wallet.BaseRPCURL
}

// guardUsageResponse prevents a bare HTTP 200 or forged cost header from being
// treated as successful upto payment. Exact EIP-3009 is unchanged.
func guardUsageResponse(resp *http.Response, signed string) error {
	raw, err := X402DecodeHeader(signed)
	if err != nil {
		return nil
	} // other provider envelope
	var envelope uptoEnvelope
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Accepted.Scheme != "upto" {
		return nil
	}
	if err = permit2.Verify(BaseChainID, envelope.Payload); err != nil {
		return err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &usageReceiptBody{ReadCloser: resp.Body, reader: bufio.NewReader(resp.Body), response: resp, envelope: envelope}
		return nil
	}
	receiptHeader := resp.Header.Get("Payment-Response")
	legacyHeader := resp.Header.Get("X-Payment-Response")
	if receiptHeader == "" {
		receiptHeader = legacyHeader
	} else if legacyHeader != "" && legacyHeader != receiptHeader {
		return fmt.Errorf("conflicting payment receipt headers")
	}
	header, err := X402DecodeHeader(receiptHeader)
	if err != nil {
		return fmt.Errorf("missing payment receipt")
	}
	var report settlementReport
	if err = json.Unmarshal(header, &report); err != nil {
		return err
	}
	if amountHeader := resp.Header.Get("X-Claw402-Settled-Amount"); amountHeader != "" {
		if report.Amount != "" && report.Amount != amountHeader {
			return fmt.Errorf("conflicting settled amounts")
		}
		report.Amount = amountHeader
	}
	if err = verifyUsageReport(resp.Request.Context(), envelope, report, receiptRPC()); err != nil {
		return err
	}
	publishVerifiedReport(resp.Header, report)
	return nil
}

func publishVerifiedReport(h http.Header, report settlementReport) {
	amount, _ := permit2.Uint256(report.Amount)
	// Keep cost integer-derived. Never trust an independent USD header.
	whole := new(big.Int).Div(new(big.Int).Set(amount), big.NewInt(1_000_000))
	fraction := new(big.Int).Mod(amount, big.NewInt(1_000_000))
	h.Set(X402SettledUSDHeader, fmt.Sprintf("%s.%06d", whole.String(), fraction.Int64()))
	h.Set("X-Nofx-Receipt-Verified", "true")
	h.Set("X-Claw402-Settled-Amount", report.Amount)
	b, _ := json.Marshal(report)
	h.Set("Payment-Response", base64.StdEncoding.EncodeToString(b))
}

// Only keepalives may precede the receipt. Verify it before exposing even one
// model chunk to a decision. This reader never sends a second paid request.
type usageReceiptBody struct {
	io.ReadCloser
	reader   *bufio.Reader
	response *http.Response
	envelope uptoEnvelope
	ready    bool
	err      error
	pending  []byte
}

func (b *usageReceiptBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.pending) > 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	if b.err != nil {
		return 0, b.err
	}
	if !b.ready {
		for total := 0; !b.ready; {
			line, err := b.reader.ReadSlice('\n')
			total += len(line)
			if err != nil || total > 64*1024 {
				b.err = &PaymentOutcomeUnknownError{Cause: fmt.Errorf("usage stream ended without a verified settlement receipt")}
				return 0, b.err
			}
			text := strings.TrimSpace(string(line))
			if text == "" || strings.HasPrefix(text, ":") {
				// Let keepalives reach the SSE parser/idle watchdog, but no model data.
				b.pending = append([]byte(nil), line...)
				n := copy(p, b.pending)
				b.pending = b.pending[n:]
				return n, nil
			}
			if text == "event: x402.settlement" {
				continue
			}
			if !strings.HasPrefix(text, "data:") {
				b.err = &PaymentOutcomeUnknownError{Cause: fmt.Errorf("usage stream missing settlement event")}
				return 0, b.err
			}
			var report settlementReport
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(text, "data:"))), &report) != nil || report.Type != "x402.settlement" {
				b.err = &PaymentOutcomeUnknownError{Cause: fmt.Errorf("usage stream missing settlement report")}
				return 0, b.err
			}
			if err = verifyUsageReport(b.response.Request.Context(), b.envelope, report, receiptRPC()); err != nil {
				b.err = &PaymentOutcomeUnknownError{Cause: err}
				return 0, b.err
			}
			publishVerifiedReport(b.response.Header, report)
			b.ready = true
		}
	}
	return b.reader.Read(p)
}
