package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nofx/mcp"
	"nofx/mcp/payment"
	"strings"
	"testing"
)

type fakeNOFX struct {
	mcp.AIClient
	req      *mcp.Request
	response *mcp.LLMResponse
	err      error
	calls    int
}

func (f *fakeNOFX) CallWithRequestFull(r *mcp.Request) (*mcp.LLMResponse, error) {
	f.req = r
	f.calls++
	return f.response, f.err
}
func TestNOFXAdapterPreservesToolRoundTripAndPrivateReasoning(t *testing.T) {
	f := &fakeNOFX{response: &mcp.LLMResponse{Content: "answer", ReasoningContent: "private reasoning", ToolCalls: []mcp.ToolCall{{ID: "call1", Type: "function", Function: mcp.ToolCallFunction{Name: "read_market", Arguments: `{}`}}}}}
	a := NOFXAdapter{Client: f}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, e := a.Chat(ctx, []Message{{Role: "assistant", ReasoningContent: "prior private"}, {Role: "tool", ToolCallID: "call0", Content: "observed"}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if f.req.Ctx != ctx || f.req.Messages[0].ReasoningContent != "prior private" || f.req.Messages[1].ToolCallID != "call0" || out.ToolCalls[0].ID != "call1" {
		t.Fatal("tool protocol lost")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private reasoning") {
		t.Fatal("reasoning leaked to persisted chat")
	}
}
func TestNOFXAdapterRejectsEmptyAndOversizedResponses(t *testing.T) {
	for _, r := range []*mcp.LLMResponse{nil, {}, {Content: strings.Repeat("x", 24001)}} {
		a := NOFXAdapter{Client: &fakeNOFX{response: r}}
		if _, e := a.Chat(context.Background(), nil, nil); e == nil {
			t.Fatal("invalid response accepted")
		}
	}
}

func TestNOFXAdapterPreservesSafePaymentFailureWithoutRetry(t *testing.T) {
	for _, err := range []error{fmt.Errorf("wrapper secret url: %w", &payment.PaymentOutcomeUnknownError{Cause: errors.New("https://secret.invalid/credential malformed HTTP response")}), &payment.PaymentVerificationRejectedError{Status: 402}} {
		f := &fakeNOFX{err: err}
		a := NOFXAdapter{Client: f}
		_, got := a.Chat(context.Background(), nil, nil)
		if got == nil || f.calls != 1 || strings.Contains(got.Error(), "secret") || strings.Contains(got.Error(), "https://") {
			t.Fatal("lost paid-call safety classification or leaked cause", got, f.calls)
		}
		var unknown *payment.PaymentOutcomeUnknownError
		if errors.As(err, &unknown) && !strings.Contains(got.Error(), "paid request outcome unknown") {
			t.Fatal("unknown payment was flattened into generic retry instruction")
		}
	}
}

func TestQuoteLimitExplainsThatPaymentWasNotSubmitted(t *testing.T) {
	f := &fakeNOFX{err: fmt.Errorf("private provider detail: %w", &payment.PaymentQuoteLimitError{QuoteMicroUSDC: 1076335})}
	a := NOFXAdapter{Client: f}
	_, err := a.Chat(context.Background(), nil, nil)
	if err == nil || f.calls != 1 || !strings.Contains(err.Error(), "1.076335 USDC") || !strings.Contains(err.Error(), "No payment was submitted") || strings.Contains(err.Error(), "private") {
		t.Fatal("quote rejection hidden or sensitive detail leaked", err)
	}
}
