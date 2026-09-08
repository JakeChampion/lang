package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ssa"
)

var voidCallCases = []struct{ name, source, want string }{
	{"void-call-empty", `function pilot(): string { noop(); return "effect"; } function noop(): void {}`, "effect\n"},
	{"void-call-owned", `function pilot(): string { sink([["released"]]); return "released"; }
function sink(own items: string[][]): void { var child = items[0]; }`, "released\n"},
	{"void-call-borrow", `function pilot(): string {
  var items = [["kept"]]; read(items[0]); return items[0][0];
}
function read(items: string[]): void { var value = items[0]; }`, "kept\n"},
	{"void-call-own-borrow-alias", `function pilot(): string { return check(["anchored"]); }
function check(own items: string[]): string { inspect(items, items); return "anchored"; }
function inspect(reader: string[], own taken: string[]): void { var value = reader[0]; }`, "anchored\n"},
	{"void-call-shared-owned-children", `function pilot(): string {
  var items = ["twice"]; sink([items], [items]); return items[0];
}
function sink(own first: string[][], own second: string[][]): void {}`, "twice\n"},
	{"void-call-recursive", `function pilot(): string { recurse(["recursive"], 3i32); return "recursive"; }
function recurse(own items: string[], n: i32): void { if (n == 0i32) { return; } recurse(items, n - 1i32); }`, "recursive\n"},
	{"void-call-return-call", `function pilot(): string { relay(["relayed"]); return "relayed"; }
function relay(own items: string[]): void { return sink(items); }
function sink(own items: string[]): void {}`, "relayed\n"},
	{"void-call-allocated-callee", `function pilot(): string { var i = 0i32; while (i < 64i32) { allocate(); i = i + 1i32; } return "balanced"; }
function allocate(): void { var parent = ((["temporary"], true), [["nested"]]); }`, "balanced\n"},
	{"void-call-argument-order", `function pilot(): string {
  var n = 0i32;
  sink({ n = n + 1i32; n }, { n = n * 2i32; n });
  if (n == 2i32) { return "ordered"; } return "wrong";
}
function sink(a: i32, b: i32): void {}`, "ordered\n"},
	{"void-call-argument-return", `function pilot(): string {
  sink({ return "argument exit"; ["dead"] }, fault()); return "wrong";
}
function sink(items: string[], n: i32): void {}
function fault(): i32 { var empty: i32[] = []; return empty[0]; }`, "argument exit\n"},
	{"void-call-argument-continue", `function pilot(): string {
  var i = 0i32; while (i < 2i32) { i = i + 1i32; sink({ continue; i }); return "wrong"; } return "continued";
}
function sink(n: i32): void {}`, "continued\n"},
}

func TestBuildVoidCallFlow(t *testing.T) {
	for _, tc := range voidCallCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, info := checkedProgram(t, tc.source)
			p, err := BuildProgram(prog, info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := planProgramUnits(p); err != nil {
				t.Fatal(err)
			}
			flow, err := solveReturnFlow(p)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range p.funcs {
				if _, fake := f.values[0]; fake {
					t.Fatal("void created semantic value zero")
				}
				if _, fake := flow.funcs[f].values[0]; fake {
					t.Fatal("void created a value provenance equation")
				}
			}
		})
	}
}

func voidEffectProgram(t *testing.T) *Program {
	t.Helper()
	prog, info := checkedProgram(t, `function pilot(own items: string[]): string {
  inspect(items, items); return "done";
}
function inspect(reader: string[], own taken: string[]): void { var value = reader[0]; }`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVoidCallPreservesEffectsWithoutAValue(t *testing.T) {
	p := voidEffectProgram(t)
	f := p.funcs[0]
	op := firstCall(t, f)
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	e := effects.ops[op]
	if op.Result != (ssa.Value{}) || e.result != resultNone || e.callee != p.funcs[1] ||
		e.inputs[0].consume || !e.inputs[1].consume || f.effectPositions[op].Line == 0 {
		t.Fatal("resultless call lost its function identity, argument obligations or source origin")
	}
	plans, err := planProgramUnits(p)
	if err != nil {
		t.Fatal(err)
	}
	step := plans[f].ops[op]
	if len(step.supplies) != 1 || step.supplies[0].mode != unitRetain {
		t.Fatal("consuming argument must retain while caller anchors the aliased borrow")
	}
	out, err := LowerARM64SSA(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, block := range out.Functions[out.Symbols["pilot"]].Blocks {
		for _, lowered := range block.Ops {
			if lowered.Kind == ssa.OpCall && lowered.Str == out.Symbols["inspect"] {
				found = true
				if lowered.Result.IsValid() || out.Positions[lowered] != f.effectPositions[op] {
					t.Fatal("physical void call invented a result or lost its source position")
				}
			}
		}
	}
	if !found {
		t.Fatal("effect-only call was erased")
	}
}

func TestVoidCallRejectsInvalidContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Program, *ssa.Op)
		want string
	}{
		{"fake-scalar-result", func(p *Program, op *ssa.Op) {
			op.Result = p.funcs[0].graph.NewValue()
			p.funcs[0].values[op.Result.ID] = valueInfo{typ: ast.NumberType{}}
		}, "arity"},
		{"fake-void-result", func(p *Program, op *ssa.Op) {
			op.Result = p.funcs[0].graph.NewValue()
			p.funcs[0].values[op.Result.ID] = valueInfo{typ: ast.VoidType{}}
		}, "unsupported semantic type"},
		{"missing-origin", func(p *Program, op *ssa.Op) { delete(p.funcs[0].effectPositions, op) }, "no source metadata"},
		{"foreign-origin", func(p *Program, _ *ssa.Op) { p.funcs[0].effectPositions[&ssa.Op{}] = ast.Position{} }, "stale effect-only"},
		{"invalid-resultless-op", func(_ *Program, op *ssa.Op) { op.Kind = ssa.OpTupleGet }, "only void semantic calls"},
		{"value-callee", func(_ *Program, op *ssa.Op) { op.Imm = 1 }, "arity"},
		{"missing-argument", func(_ *Program, op *ssa.Op) { op.Args = nil }, "arity"},
		{"zero-argument", func(_ *Program, op *ssa.Op) { op.Args[0] = ssa.Value{} }, "invalid or foreign"},
		{"missing-callee", func(_ *Program, op *ssa.Op) { op.Imm = 0 }, "function identity"},
		{"stale-own", func(p *Program, _ *ssa.Op) { p.funcs[1].contract.modes[1] = ParamBorrow }, "call contract"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := voidEffectProgram(t)
			tc.edit(p, firstCall(t, p.funcs[0]))
			if err := VerifyProgram(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestVoidCalleeStillRequiresBalancedOwnership(t *testing.T) {
	p := voidEffectProgram(t)
	plans, err := planProgramUnits(p)
	if err != nil {
		t.Fatal(err)
	}
	callee := p.funcs[1]
	plans[callee].entry[callee.graph.Entry] = nil
	if err := verifyProgramUnits(p, plans); err == nil {
		t.Fatal("void callee leaked its counted parameter without rejection")
	}
}

func TestVoidCallCannotSupplyAValue(t *testing.T) {
	p := voidEffectProgram(t)
	f := p.funcs[0]
	b := builder{fn: f, info: &checker.Info{}, current: f.graph.Entry}
	call := &ast.Call{Callee: &ast.Ident{Name: "inspect"}}
	before := len(b.current.Ops)
	if _, err := b.expr(call); err == nil || !strings.Contains(err.Error(), "cannot supply a semantic value") {
		t.Fatalf("void call used as a value: %v", err)
	}
	if len(b.current.Ops) != before {
		t.Fatal("invalid value context emitted call effects")
	}
}

func TestVoidCallRepeatedUnitsAreIndependent(t *testing.T) {
	prog, info := checkedProgram(t, `function pilot(own first: string[], own second: string[]): string {
  sink(first, second); return "done";
}
function sink(own first: string[], own second: string[]): void {}`)
	p, err := BuildProgram(prog, info)
	if err != nil {
		t.Fatal(err)
	}
	f := p.funcs[0]
	call := firstCall(t, f)
	// A semantic transform may expose repeated value identities even though
	// the source checker forbids using an own binding after it was moved.
	call.Args[1] = call.Args[0]
	plans, err := planProgramUnits(p)
	if err != nil {
		t.Fatal(err)
	}
	step := plans[f].ops[call]
	if len(step.supplies) != 2 || step.supplies[0].mode != unitRetain || step.supplies[1].mode != unitMove {
		t.Fatalf("repeated consuming arguments need distinct units: %+v", step.supplies)
	}
	step.supplies[0].mode = unitMove
	plans[f].ops[call] = step
	if err := verifyProgramUnits(p, plans); err == nil {
		t.Fatal("one unit was transferred twice to a void call")
	}
}

func TestARM64TypedVoidCallsKeepObservableEffects(t *testing.T) {
	armLauncher(t)
	for _, optimize := range []bool{false, true} {
		out := lowerCheckedARM64(t, `function pilot(): string { fault(); return "call erased"; }
function fault(): void { var empty: i32[] = []; var value = empty[0]; }`)
		stdout, stderr, code := runARM64Pilot(t, armExecutable(t, out, printHarness(out), optimize))
		if code != 134 || stdout != "" {
			t.Fatalf("optimized=%v: exit %d stdout %q stderr %q; expected bounds abort", optimize, code, stdout, stderr)
		}
	}
}
