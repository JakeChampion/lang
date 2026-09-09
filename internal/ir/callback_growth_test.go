package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

func TestCallbackGrowthEffects(t *testing.T) {
	const src = `struct State { xs: i32[] }
struct Nested { state: State }
struct Callback { run: (State) => State }
function pure(s: State): State { return s; }
function array(xs: i32[], cb: (i32[]) => i32[]): i32[] { return cb(xs); }
function record(s: State, cb: (State) => State): State { return cb(s); }
function renamed(s: State, cb: (State) => State): State { var alias = s; return cb(alias); }
function local(s: State, cb: (State) => State): State { var f = cb; return f(s); }
function collision(s: State, pure: (State) => State): State { return pure(s); }
function field(s: State, cb: (i32[]) => i32[]): i32[] { return cb(s.xs); }
function nested(s: Nested, cb: (i32[]) => i32[]): i32[] { return cb(s.state.xs); }
function fieldcall(s: State, cb: Callback): State { return cb.run(s); }
function indexed(s: State, cbs: ((State) => State)[]): State { return cbs[0](s); }
function choose(cb: (State) => State): (State) => State { return cb; }
function returned(s: State, cb: (State) => State): State { return choose(cb)(s); }
function direct(s: State): State { return pure(s); }
function scalar(n: i32, cb: (i32) => i32): i32 { return cb(n); }
function main(): i32 { return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	grow := computeGrowParams(prog, info, computeParamFieldObs(prog, nil))
	for _, tc := range []struct {
		name   string
		buffer bool
		field  string
	}{
		{"array", true, ""},
		{"record", false, growAnyField},
		{"renamed", false, growAnyField},
		{"local", false, growAnyField},
		{"collision", false, growAnyField},
		{"field", false, "xs"},
		{"nested", false, growAnyField},
		{"fieldcall", false, growAnyField},
		{"indexed", false, growAnyField},
		{"returned", false, growAnyField},
		{"pure", false, ""},
		{"direct", false, ""},
		{"scalar", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := grow[tc.name][0]
			if g.buffer != tc.buffer || (tc.field == "" && len(g.fields) != 0) ||
				(tc.field != "" && (len(g.fields) != 1 || !g.fields[tc.field])) {
				t.Errorf("growth = %+v, want buffer=%v field=%q", g, tc.buffer, tc.field)
			}
		})
	}
	// Closure conversion replaces a captured callable with CaptureRef.
	for _, fn := range prog.Funcs {
		if fn.Name == "record" {
			ast.Walk(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.Call); ok {
					c.Callee = &ast.CaptureRef{}
				}
				return true
			})
		}
	}
	if g := computeGrowParams(prog, info, nil)["record"][0]; !g.growsField("xs") {
		t.Errorf("captured callback lost growth: %+v", g)
	}
}
