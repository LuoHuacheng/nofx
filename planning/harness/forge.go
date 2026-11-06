package harness

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"nofx/planning/core"
	"nofx/planning/forge"
	"nofx/planning/model"
	"time"
)

const forgeGuide = `Pure entry development v1: restricted Starlark (Python-like), not Go/shell/Python. Define plan(market,args) returning {ideas:[...]} with 0-5 ideas using the propose_plans schema; prices and weights are decimal strings. market has venue/symbol/bid/ask/mid/tick as strings. No files, network, imports/load, attributes/methods, comprehensions, lambda or recursion. No binary +, *, %, powers or bitwise operations; use bounded add(a,b) and mul(a,b); subtraction/division are allowed. float/int/str convert finite numbers; len; fail(message) rejects invalid arguments; min/max accept 2 numbers. range(n): 0-64; append(list,value): maximum128. price(value,market['tick']) rounds DOWN to tick and returns a decimal string. allocate_weights([1,2,4]) returns 6-place decimal strings summing EXACTLY to 1. Maximum source16KiB, 20000 interpreter steps, 2seconds in a separate minimal-environment process. Up to64 immutable versions scoped to the current session. develop_entry_plugin requires2-6 tests with at least one successful exact output. Tests run on FIXTURE market: venue=binance,symbol=BTCUSDT,bid=99999.9,ask=100000.1,mid=100000,tick=0.1. Each test supplies args_json and expected_json strings; expected_json uses {ideas:[...]}; for expect_error=true expected_json can be empty. All tests must pass to mount. Inspect reports and correct failures. run_entry_plugin takes the EXACT hash and args_json and uses a fresh PUBLIC book. Its returned ideas still require propose_plans validation. No order permission, no new order trigger types, no recurring grid or dynamic repricing. Test success proves software behavior only, not financial feasibility or profit.`

func forgeTools() []model.Tool {
	text := map[string]any{"type": "string"}
	tests := object(map[string]any{"name": text, "args_json": text, "expected_json": text, "expect_error": map[string]any{"type": "boolean"}}, "name", "args_json", "expected_json", "expect_error")
	specs := []struct {
		name, description string
		schema            any
	}{
		{"get_entry_plugins", "Read the calculator development guide and installed versions; optional hash reads one source/test package.", object(map[string]any{"hash": text})},
		{"develop_entry_plugin", "Write and test a pure entry calculator; failed packages cannot run. Passing tests mounts an immutable version. No plan save or trade authority.", object(map[string]any{"id": text, "description": text, "source": text, "tests": map[string]any{"type": "array", "items": tests, "minItems": 2, "maxItems": 6}}, "id", "description", "source", "tests")},
		{"run_entry_plugin", "Run an enabled tested calculator hash on fresh public market inputs. Returns ideas only; propose_plans separately validates risk.", object(map[string]any{"hash": text, "args_json": text}, "hash", "args_json")},
	}
	out := []model.Tool{}
	for _, s := range specs {
		var t model.Tool
		t.Type = "function"
		t.Function.Name = s.name
		t.Function.Description = s.description
		t.Function.Parameters = s.schema
		out = append(out, t)
	}
	return out
}
func (e *Engine) forgeCatalog(session string) []map[string]any {
	out := []map[string]any{}
	for _, a := range e.Forge.List(session) {
		out = append(out, map[string]any{"id": a.ID, "description": a.Description, "hash": a.Hash, "status": a.Status, "enabled": a.Enabled})
	}
	return out
}
func argsObject(raw string) (map[string]any, error) {
	if len(raw) > 8192 {
		return nil, errors.New("Plugin arguments exceed limits")
	}
	var args map[string]any
	if json.Unmarshal([]byte(raw), &args) != nil || args == nil {
		return nil, errors.New("args_json must be a JSON object")
	}
	return args, nil
}
func (e *Engine) invokeForge(ctx context.Context, input Input, call model.ToolCall, emit Emit) (any, error) {
	if e.Forge == nil || input.SessionID == "" {
		return nil, errors.New("No entry development workspace exists for this session")
	}
	switch call.Function.Name {
	case "get_entry_plugins":
		var args struct {
			Hash string `json:"hash"`
		}
		if err := strict(call.Function.Arguments, &args); err != nil {
			return nil, err
		}
		result := map[string]any{"guide": forgeGuide, "plugins": e.forgeCatalog(input.SessionID)}
		if args.Hash != "" {
			a, err := e.Forge.Get(input.SessionID, args.Hash)
			if err != nil {
				return nil, err
			}
			result["package"] = a
		}
		return result, nil
	case "develop_entry_plugin":
		var args struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Source      string `json:"source"`
			Tests       []struct {
				Name        string `json:"name"`
				Args        string `json:"args_json"`
				Expected    string `json:"expected_json"`
				ExpectError bool   `json:"expect_error"`
			} `json:"tests"`
		}
		if err := strict(call.Function.Arguments, &args); err != nil {
			return nil, err
		}
		if HasCredential(args.Source) {
			return nil, errors.New("Plugin code cannot contain credentials")
		}
		d := forge.Definition{ID: args.ID, Description: args.Description, Source: args.Source}
		for _, t := range args.Tests {
			params, err := argsObject(t.Args)
			if err != nil {
				return nil, err
			}
			test := forge.TestCase{Name: t.Name, Args: params, ExpectError: t.ExpectError}
			if !t.ExpectError {
				test.Expected = json.RawMessage(t.Expected)
			}
			d.Tests = append(d.Tests, test)
		}
		a, err := e.Forge.Develop(ctx, input.SessionID, d)
		if err != nil {
			return nil, err
		}
		if err = emit(Event{"forge", map[string]any{"id": a.ID, "hash": a.Hash, "status": a.Status, "enabled": a.Enabled, "reports": a.Reports}}); err != nil {
			return nil, err
		}
		return map[string]any{"id": a.ID, "hash": a.Hash, "status": a.Status, "enabled": a.Enabled, "reports": a.Reports, "no_trade_authority": true}, nil
	case "run_entry_plugin":
		var args struct {
			Hash string `json:"hash"`
			Args string `json:"args_json"`
		}
		if err := strict(call.Function.Arguments, &args); err != nil {
			return nil, err
		}
		params, err := argsObject(args.Args)
		if err != nil {
			return nil, err
		}
		snapshot, err := e.Market.Snapshot(ctx, input.Venue, input.Symbol)
		if err != nil {
			return nil, err
		}
		if err = snapshot.Validate(time.Now().UTC()); err != nil {
			return nil, err
		}
		bid, _ := new(big.Rat).SetString(string(snapshot.Bid))
		ask, _ := new(big.Rat).SetString(string(snapshot.Ask))
		mid := new(big.Rat).Quo(new(big.Rat).Add(bid, ask), big.NewRat(2, 1)).FloatString(18)
		market := map[string]any{"venue": snapshot.Venue, "symbol": snapshot.Symbol, "bid": string(snapshot.Bid), "ask": string(snapshot.Ask), "mid": mid, "tick": string(snapshot.Tick)}
		out, err := e.Forge.Run(ctx, input.SessionID, args.Hash, market, params)
		if err != nil {
			return nil, err
		}
		a, err := e.Forge.Get(input.SessionID, args.Hash)
		if err != nil {
			return nil, err
		}
		for _, idea := range out.Ideas {
			input.Generated[encoded(idea)] = core.Analysis{PluginID: a.ID, PluginHash: a.Hash}
		}
		if err = emit(Event{"forge", map[string]any{"id": a.ID, "hash": a.Hash, "status": "ran", "count": len(out.Ideas)}}); err != nil {
			return nil, err
		}
		return map[string]any{"ideas": out.Ideas, "plugin_id": a.ID, "plugin_hash": a.Hash, "needs_fresh_risk_validation": true, "no_trade_authority": true}, nil
	}
	return nil, errors.New("unknown development tool")
}
