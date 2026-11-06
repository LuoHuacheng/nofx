package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"nofx/mcp"
)

func textToolFixture() []Tool {
	var tool Tool
	tool.Type = "function"
	tool.Function.Name = "propose_plans"
	tool.Function.Parameters = map[string]any{"type": "object"}
	return []Tool{tool}
}

func TestTextToolAdapterRoundTripWithoutNativeFields(t *testing.T) {
	tools := textToolFixture()
	f := &fakeNOFX{response: &mcp.LLMResponse{Content: `{"content":"Validate a conditional candidate","tool_calls":[{"name":"propose_plans","arguments":{"ideas":[]}}]}`}}
	adapter := NOFXAdapter{Client: f, TextTools: true}
	response, err := adapter.Chat(context.Background(), []Message{{Role: "system", Content: "Draft plans only"}, {Role: "user", Content: "Plan a trade"}}, tools)
	if err != nil || len(response.ToolCalls) != 1 {
		t.Fatalf("round one: %v %+v", err, response)
	}
	if len(f.req.Tools) != 0 || f.req.ToolChoice != "" || !strings.Contains(f.req.Messages[0].Content, "propose_plans") {
		t.Fatal("native tools were sent or tool guide missing")
	}
	first := response.ToolCalls[0].ID
	_, err = adapter.Chat(context.Background(), []Message{{Role: "system", Content: "Draft only"}, response, {Role: "tool", ToolCallID: first, Content: `{"plans":[],"no_trade":true}`}}, tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range f.req.Messages {
		if message.Role == "tool" || len(message.ToolCalls) > 0 || message.ToolCallID != "" {
			t.Fatal("native tool history leaked onto a text-only endpoint")
		}
	}
	next, err := adapter.Chat(context.Background(), nil, tools)
	if err != nil || next.ToolCalls[0].ID == first {
		t.Fatal("call IDs must be locally unique")
	}
	f.response = &mcp.LLMResponse{Content: `{"content":"No draft passed validation. No trade.","tool_calls":[]}`}
	final, err := adapter.Chat(context.Background(), nil, nil)
	if err != nil || len(final.ToolCalls) != 0 || !strings.Contains(final.Content, "No trade") {
		t.Fatal("final answer lost", err)
	}
}

func TestTextToolParserRejectsInvalidAndUnauthorizedCalls(t *testing.T) {
	for _, text := range []string{
		`{"content":"","tool_calls":[{"name":"execute_order","arguments":{}}]}`,
		`{"content":"","tool_calls":[{"name":"propose_plans","arguments":[]}]}`,
		`{"content":"","tool_calls":[{"name":"propose_plans","arguments":null}]}`,
		`{"content":"","tool_calls":[]}`,
		`{"content":"ok","tool_calls":[],"execute":true}`,
		`{"content":"ok","tool_calls":[]} {"content":"second"}`,
		`not valid JSON`,
	} {
		got, err := parseTextTools(text, textToolFixture(), 1)
		if err == nil || len(got.ToolCalls) != 0 {
			t.Fatalf("invalid response accepted: %q", text)
		}
	}
	calls := make([]map[string]any, 6)
	for i := range calls {
		calls[i] = map[string]any{"name": "propose_plans", "arguments": map[string]any{}}
	}
	raw, _ := json.Marshal(map[string]any{"content": "", "tool_calls": calls})
	if _, err := parseTextTools(string(raw), textToolFixture(), 1); err == nil {
		t.Fatal("excess calls accepted")
	}
}

func TestTextToolsAreSelectedBeforePaymentOnlyForKnownRoutes(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		want            bool
	}{
		{"claw402", "gpt-5.6", true}, {"claw402", "gpt-6", true}, {"claw402", "", true},
		{"claw402", "/api/v1/ai/openai/chat/5.6", true}, {"claw402", "deepseek-v4-pro", false},
		{"deepseek", "gpt-5.6", false},
	} {
		if RequiresTextTools(tc.provider, tc.model) != tc.want {
			t.Fatalf("capability mismatch: %+v", tc)
		}
	}
}
