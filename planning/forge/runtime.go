// Package forge runs generated, pure entry calculators. Scripts receive only
// explicit public inputs; they have no file, network, process or trade handles.
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"nofx/planning/core"
)

const MaxSource = 16384
const maxOutput = 32768

type Invocation struct {
	Source string         `json:"source"`
	Market map[string]any `json:"market"`
	Args   map[string]any `json:"args"`
}
type Output struct {
	Ideas []core.Idea `json:"ideas"`
}
type workerReply struct {
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// The worker is the same compiled Go binary with a minimal, separate process
// environment. It exits before the application opens state, .env or listeners.
func init() {
	if os.Getenv("VERGEX_FORGE_WORKER") == "1" {
		code := workerMain()
		os.Exit(code)
	}
}
func workerMain() (code int) {
	defer func() {
		if recover() != nil {
			fmt.Fprint(os.Stdout, `{"error":"Plugin execution failed"}`)
			code = 1
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil || len(raw) > 65536 {
		return 1
	}
	var in Invocation
	if json.Unmarshal(raw, &in) != nil {
		return 1
	}
	out, err := evaluate(in)
	reply := workerReply{Output: out}
	if err != nil {
		reply = workerReply{Error: err.Error()}
	}
	b, _ := json.Marshal(reply)
	if len(b) > maxOutput+4096 {
		return 1
	}
	_, err = os.Stdout.Write(b)
	if err != nil {
		return 1
	}
	return 0
}

type cappedBuffer struct {
	b   bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.b.Len()+len(p) > b.max {
		return 0, errors.New("Plugin output exceeds limits")
	}
	return b.b.Write(p)
}
func Run(ctx context.Context, in Invocation) (Output, error) {
	if err := ValidateSource(in.Source); err != nil {
		return Output{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 65536 {
		return Output{}, errors.New("Plugin input exceeds limits")
	}
	executable, err := os.Executable()
	if err != nil {
		return Output{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable)
	command.Env = []string{"VERGEX_FORGE_WORKER=1", "GOMAXPROCS=1", "GOMEMLIMIT=32MiB"}
	command.Stdin = bytes.NewReader(raw)
	stdout := &cappedBuffer{max: maxOutput + 4096}
	stderr := &cappedBuffer{max: 4096}
	command.Stdout = stdout
	command.Stderr = stderr
	if err = command.Run(); err != nil {
		if ctx.Err() != nil {
			return Output{}, errors.New("Plugin execution cancelled or timed out")
		}
		return Output{}, errors.New("Plugin worker failed or exceeded output limits")
	}
	var reply workerReply
	if json.Unmarshal(stdout.b.Bytes(), &reply) != nil {
		return Output{}, errors.New("Invalid plugin worker response")
	}
	if reply.Error != "" {
		return Output{}, errors.New(reply.Error)
	}
	return decodeOutput(reply.Output)
}
func decodeOutput(raw []byte) (Output, error) {
	var out Output
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&out); err != nil {
		return out, errors.New("Plugin must return {ideas:[...]}; invalid fields or types")
	}
	if d.Decode(new(any)) != io.EOF {
		return out, errors.New("Plugin output contains trailing data")
	}
	if out.Ideas == nil || len(out.Ideas) > 5 {
		return out, errors.New("Plugin must return 0-5 entry ideas")
	}
	for _, idea := range out.Ideas {
		if idea.Execution != nil {
			switch idea.Execution.Kind {
			case "limit", "ladder", "twap":
			default:
				return out, errors.New("Plugin returned an unsupported execution type")
			}
		}
		if len(idea.Name) > 120 || len(idea.Rationale) > 1200 {
			return out, errors.New("Plugin idea text is too long")
		}
		if idea.Execution != nil && len(idea.Execution.Levels) > 8 {
			return out, errors.New("Plugin has more than 8 levels")
		}
	}
	return out, nil
}

// Resource safety is enforced by the dialect as well as the worker: no sequence
// repetition/concatenation, attributes, imports, comprehensions, unbounded range,
// arbitrary integers or unchecked container growth. Numeric add/mul and append
// are bounded host functions. An isolated process alone is not a memory sandbox.
func ValidateSource(source string) error {
	if len(source) == 0 || len(source) > MaxSource {
		return errors.New("Plugin source must contain 1-16384 bytes")
	}
	file, err := syntax.Parse("entry.star", source, 0)
	if err != nil {
		return fmt.Errorf("Plugin syntax: %w", err)
	}
	nodes := 0
	var invalid error
	allowed := map[string]bool{"None": true, "True": true, "False": true, "len": true, "str": true, "float": true, "int": true, "range": true, "fail": true, "min": true, "max": true}
	syntax.Walk(file, func(n syntax.Node) bool {
		if n == nil {
			return true
		}
		nodes++
		if nodes > 1000 {
			invalid = errors.New("Plugin source structure exceeds limits")
			return false
		}
		switch n := n.(type) {
		case *syntax.LoadStmt, *syntax.DotExpr, *syntax.Comprehension, *syntax.LambdaExpr:
			invalid = errors.New("Imports, attribute methods, comprehensions and lambdas are forbidden")
		case *syntax.BinaryExpr:
			if strings.Contains(" + * ** << >> % | & ^ ", " "+n.Op.String()+" ") {
				invalid = errors.New("Use bounded add/mul; sequence concatenation, repetition and bitwise operations are forbidden")
			}
		case *syntax.AssignStmt:
			if n.Op != syntax.EQ {
				invalid = errors.New("Augmented assignment is forbidden; use bounded add/mul")
			}
		case *syntax.Literal:
			if len(n.Raw) > 2048 {
				invalid = errors.New("Plugin literal exceeds limits")
			}
			if n.Token == syntax.INT {
				v, ok := new(big.Int).SetString(strings.ReplaceAll(n.Raw, "_", ""), 0)
				if !ok || v.BitLen() > 40 {
					invalid = errors.New("Plugin integer literal exceeds limits")
				}
			}
		case *syntax.Ident:
			if _, builtin := starlark.Universe[n.Name]; builtin && !allowed[n.Name] {
				invalid = errors.New("Plugin builtin is forbidden: " + n.Name)
			}
		}
		return invalid == nil
	})
	return invalid
}
func number(v starlark.Value) (float64, error) {
	var x float64
	switch v := v.(type) {
	case starlark.Int:
		f := v.Float()
		x = float64(f)
	case starlark.Float:
		x = float64(v)
	case starlark.String:
		var err error
		x, err = strconv.ParseFloat(string(v), 64)
		if err != nil {
			return 0, errors.New("Number required")
		}
	default:
		return 0, errors.New("Number required")
	}
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1e12 {
		return 0, errors.New("Number exceeds plugin calculation bounds")
	}
	return x, nil
}
func builtin(name string, fn func(starlark.Tuple) (starlark.Value, error)) *starlark.Builtin {
	return starlark.NewBuiltin(name, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kw []starlark.Tuple) (starlark.Value, error) {
		if len(kw) > 0 {
			return nil, errors.New("Keyword arguments are unsupported")
		}
		return fn(args)
	})
}
func safeBuiltins() starlark.StringDict {
	values := starlark.StringDict{"len": starlark.Universe["len"]}
	values["fail"] = builtin("fail", func(a starlark.Tuple) (starlark.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("fail requires an explanation")
		}
		s, ok := starlark.AsString(a[0])
		if !ok || len(s) > 500 {
			return nil, errors.New("Invalid fail explanation")
		}
		return nil, errors.New(s)
	})
	for _, name := range []string{"add", "mul", "min", "max"} {
		op := name
		values[op] = builtin(op, func(a starlark.Tuple) (starlark.Value, error) {
			if len(a) != 2 {
				return nil, errors.New("Two numbers required")
			}
			x, err := number(a[0])
			if err != nil {
				return nil, err
			}
			y, err := number(a[1])
			if err != nil {
				return nil, err
			}
			z := x + y
			switch op {
			case "mul":
				z = x * y
			case "min":
				z = math.Min(x, y)
			case "max":
				z = math.Max(x, y)
			}
			if _, err := number(starlark.Float(z)); err != nil {
				return nil, err
			}
			return starlark.Float(z), nil
		})
	}
	for _, name := range []string{"float", "int", "str"} {
		op := name
		values[op] = builtin(op, func(a starlark.Tuple) (starlark.Value, error) {
			if len(a) != 1 {
				return nil, errors.New("One argument required")
			}
			if op == "str" {
				if s, ok := a[0].(starlark.String); ok {
					return s, nil
				}
			}
			x, err := number(a[0])
			if err != nil {
				return nil, err
			}
			switch op {
			case "int":
				return starlark.MakeInt64(int64(x)), nil
			case "str":
				return starlark.String(strconv.FormatFloat(x, 'f', -1, 64)), nil
			}
			return starlark.Float(x), nil
		})
	}
	values["range"] = builtin("range", func(a starlark.Tuple) (starlark.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("range accepts one argument only")
		}
		n, err := number(a[0])
		if err != nil || n != math.Trunc(n) || n < 0 || n > 64 {
			return nil, errors.New("range must be between 0 and 64")
		}
		out := []starlark.Value{}
		for i := 0; i < int(n); i++ {
			out = append(out, starlark.MakeInt(i))
		}
		return starlark.NewList(out), nil
	})
	values["append"] = builtin("append", func(a starlark.Tuple) (starlark.Value, error) {
		if len(a) != 2 {
			return nil, errors.New("append requires a list and an element")
		}
		l, ok := a[0].(*starlark.List)
		if !ok || l.Len() >= 128 {
			return nil, errors.New("List may contain at most 128 items")
		}
		if err := l.Append(a[1]); err != nil {
			return nil, err
		}
		return starlark.None, nil
	})
	values["price"] = builtin("price", func(a starlark.Tuple) (starlark.Value, error) {
		if len(a) != 2 {
			return nil, errors.New("price(value,tick)")
		}
		x, err := number(a[0])
		if err != nil {
			return nil, err
		}
		tick, ok := starlark.AsString(a[1])
		if !ok {
			return nil, errors.New("tick must be a decimal string")
		}
		step, ok := new(big.Rat).SetString(tick)
		if !ok || step.Sign() <= 0 {
			return nil, errors.New("Invalid tick")
		}
		v, _ := new(big.Rat).SetString(strconv.FormatFloat(x, 'f', -1, 64))
		ratio := new(big.Rat).Quo(v, step)
		whole := new(big.Int).Quo(ratio.Num(), ratio.Denom())
		result := new(big.Rat).Mul(new(big.Rat).SetInt(whole), step)
		s := strings.TrimRight(strings.TrimRight(result.FloatString(18), "0"), ".")
		return starlark.String(s), nil
	})
	values["allocate_weights"] = builtin("allocate_weights", func(a starlark.Tuple) (starlark.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("allocate_weights requires a list")
		}
		l, ok := a[0].(*starlark.List)
		if !ok || l.Len() < 2 || l.Len() > 8 {
			return nil, errors.New("Weights require 2-8 items")
		}
		weights := []float64{}
		sum := 0.
		for i := 0; i < l.Len(); i++ {
			v, err := number(l.Index(i))
			if err != nil || v <= 0 {
				return nil, errors.New("Weights must be positive")
			}
			weights = append(weights, v)
			sum += v
		}
		left := 1000000
		out := []starlark.Value{}
		for i, w := range weights {
			units := int(math.Floor(w / sum * 1000000))
			if i == len(weights)-1 {
				units = left
			}
			if units <= 0 {
				return nil, errors.New("Weight is too small")
			}
			left -= units
			out = append(out, starlark.String(strconv.FormatFloat(float64(units)/1000000, 'f', 6, 64)))
		}
		return starlark.NewList(out), nil
	})
	return values
}
func toStar(v any, depth int, nodes *int) (starlark.Value, error) {
	*nodes++
	if *nodes > 1024 || depth > 12 {
		return nil, errors.New("Plugin input structure exceeds limits")
	}
	switch v := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(v), nil
	case string:
		if len(v) > 2048 {
			return nil, errors.New("Plugin input text is too long")
		}
		return starlark.String(v), nil
	case float64:
		if _, err := number(starlark.Float(v)); err != nil {
			return nil, err
		}
		return starlark.Float(v), nil
	case map[string]any:
		if len(v) > 64 {
			return nil, errors.New("Plugin input dictionary exceeds limits")
		}
		d := starlark.NewDict(len(v))
		for k, x := range v {
			z, err := toStar(x, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			if err = d.SetKey(starlark.String(k), z); err != nil {
				return nil, err
			}
		}
		return d, nil
	case []any:
		if len(v) > 128 {
			return nil, errors.New("Plugin input list exceeds limits")
		}
		a := []starlark.Value{}
		for _, x := range v {
			z, err := toStar(x, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			a = append(a, z)
		}
		return starlark.NewList(a), nil
	}
	return nil, errors.New("Plugin input is not JSON data")
}
func toJSON(v starlark.Value, depth int, nodes *int) (any, error) {
	*nodes++
	if *nodes > 2048 || depth > 12 {
		return nil, errors.New("Plugin output exceeds limits or has cyclic references")
	}
	switch v := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(v), nil
	case starlark.String:
		if len(v) > 2048 {
			return nil, errors.New("Plugin output text is too long")
		}
		return string(v), nil
	case starlark.Int, starlark.Float:
		return number(v)
	case *starlark.List:
		if v.Len() > 128 {
			return nil, errors.New("Plugin output list exceeds limits")
		}
		a := []any{}
		for i := 0; i < v.Len(); i++ {
			x, err := toJSON(v.Index(i), depth+1, nodes)
			if err != nil {
				return nil, err
			}
			a = append(a, x)
		}
		return a, nil
	case *starlark.Dict:
		if v.Len() > 64 {
			return nil, errors.New("Plugin output dictionary exceeds limits")
		}
		d := map[string]any{}
		for _, pair := range v.Items() {
			k, ok := starlark.AsString(pair[0])
			if !ok || len(k) > 100 {
				return nil, errors.New("Invalid plugin dictionary key")
			}
			x, err := toJSON(pair[1], depth+1, nodes)
			if err != nil {
				return nil, err
			}
			d[k] = x
		}
		return d, nil
	}
	return nil, errors.New("Plugin output includes non-JSON values")
}
func evaluate(in Invocation) (json.RawMessage, error) {
	if err := ValidateSource(in.Source); err != nil {
		return nil, err
	}
	nodes := 0
	m, err := toStar(in.Market, 0, &nodes)
	if err != nil {
		return nil, err
	}
	m.Freeze()
	a, err := toStar(in.Args, 0, &nodes)
	if err != nil {
		return nil, err
	}
	a.Freeze()
	thread := &starlark.Thread{Name: "entry calculator"}
	thread.SetMaxExecutionSteps(20000)
	globals, err := starlark.ExecFile(thread, "entry.star", in.Source, safeBuiltins())
	if err != nil {
		return nil, err
	}
	fn, ok := globals["plan"]
	if !ok {
		return nil, errors.New("Plugin requires def plan(market, args)")
	}
	result, err := starlark.Call(thread, fn, starlark.Tuple{m, a}, nil)
	if err != nil {
		return nil, err
	}
	nodes = 0
	data, err := toJSON(result, 0, &nodes)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > maxOutput {
		return nil, errors.New("Plugin output exceeds limits")
	}
	if _, err = decodeOutput(raw); err != nil {
		return nil, err
	}
	return raw, nil
}
