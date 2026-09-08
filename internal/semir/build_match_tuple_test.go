package semir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ssa"
)

var tupleMatchCases = []struct{ name, source, want string }{
	{"tuple-match-only-wildcard", `function pilot(): string { return match ((["held"], true)) { _ => "wildcard" }; }`, "wildcard\n"},
	{"tuple-match-only-guarded-wildcards", `function pilot(): string {
  var items = ["before"];
  return match ((1i32, true)) { _ when { items = ["wildcard guard"]; false } => "wrong", _ => items[0] };
}`, "wildcard guard\n"},
	{"tuple-match-literal", `function pilot(): string { return choose((1i32, true)); }
function choose(pair: (i32, boolean)): string {
  return match (pair) { (0i32, _) => "wrong", (1i32, true) => "selected", _ => "wrong" };
}`, "selected\n"},
	{"tuple-match-later-element-failure", `function pilot(): string { return choose((1i32, false)); }
function choose(pair: (i32, boolean)): string {
  return match (pair) { (1i32, true) => "wrong", (n, flag) => if (n == 1i32 && !flag) { "fallback" } else { "wrong" } };
}`, "fallback\n"},
	{"tuple-match-nested", `function pilot(): string {
  return match ((9i32, (true, ["nested"]))) { (_, (false, _)) => "wrong", (_, (true, items)) => items[0], _ => "wrong" };
}`, "nested\n"},
	{"tuple-match-nested-irrefutable", `function pilot(): string {
  return match ((9i32, (true, ["bound"]))) { (_, (_, items)) => items[0] };
}`, "bound\n"},
	{"tuple-match-at-binding", `function pilot(): string {
  return match ((9i32, ["whole"])) { whole @ (n, _) => { let (_, other) = whole; if (n == 9i32) { other[0] } else { "wrong" } } };
}`, "whole\n"},
	{"tuple-match-pattern-shadow", `function pilot(): string {
  var items = ["outer"];
  return match ((["inner"], false)) { (items, true) => "wrong", _ => items[0] };
}`, "outer\n"},
	{"tuple-match-guard-shadow", `function pilot(): string {
  var items = ["outer"];
  return match ((["inner"], true)) { (items, _) when { items = ["changed local"]; false } => "wrong", _ => items[0] };
}`, "outer\n"},
	{"tuple-match-false-guard-state", `function pilot(): string {
  var parent = (["old"], true);
  return match (parent) {
    (items, _) when { parent = (["replacement"], false); false } => "wrong",
    (items, _) => { let (_, flag) = parent; if (!flag) { items[0] } else { "wrong" } }
  };
}`, "old\n"},
	{"tuple-match-guarded-wildcard", `function pilot(): string {
  var items = ["before"];
  return match ((1i32, true)) { _ when { items = ["guard"]; false } => "wrong", (_, _) => items[0] };
}`, "guard\n"},
	{"tuple-match-scrutinee-once", `function pilot(): string {
  var count = 0i32;
  var items = match ({ count = count + 1i32; (count, ["once"]) }) { (0i32, _) => ["wrong"], (_, items) => items };
  if (count == 1i32) { return items[0]; } return "repeated";
}`, "once\n"},
	{"tuple-match-escaped-child", `function pilot(): string {
  var items = match ((true, (["held"], 2i32))) { (true, (child, _)) => child, _ => ["wrong"] };
  var i = 0i32; while (i < 64i32) { var churn = ((["churn"], 2i32), false); i = i + 1i32; }
  return items[0];
}`, "held\n"},
	{"tuple-match-own-argument", `function pilot(): string {
  var snapshot = ["shared"]; var child = choose((snapshot, true));
  var i = 0i32; while (i < 64i32) { var churn = (["churn"], false); i = i + 1i32; }
  return [snapshot[0], child[0]][1];
}
function choose(own pair: (string[], boolean)): string[] { return match (pair) { (items, _) => items }; }`, "shared\n"},
	{"tuple-match-borrow-argument", `function pilot(): string {
  var child = choose((["borrowed"], false));
  var i = 0i32; while (i < 64i32) { var churn = (["churn"], false); i = i + 1i32; }
  return child[0];
}
function choose(pair: (string[], boolean)): string[] { return match (pair) { (items, _) => items }; }`, "borrowed\n"},
	{"tuple-match-guard-return", `function pilot(): string {
  return match ((["guard exit"], true)) { (items, flag) when { if (flag) { return items[0]; } false } => "wrong", _ => "wrong" };
}`, "guard exit\n"},
	{"tuple-match-body-return", `function pilot(): string {
  return match ((["body exit"], true)) { (items, _) => { return items[0]; } };
}`, "body exit\n"},
	{"tuple-match-guard-no-continuation", `function pilot(): string {
  return match ((["ended guard"], true)) { (items, _) when { return items[0]; true } => "dead body", _ => "dead fallback" };
}`, "ended guard\n"},
	{"tuple-match-guard-no-continuation-join", `function pilot(): string { return choose(true); }
function choose(flag: boolean): string {
  var items = match ((flag, ["live"])) { (true, child) => child, (_, child) when { return "guard exit"; true } => child, _ => ["dead fallback"] };
  return items[0];
}`, "live\n"},
	{"tuple-match-continue", `function pilot(): string {
  var i = 0i32; var items = ["before"];
  while (i < 2i32) {
    i = i + 1i32;
    var selected = match ((i, ["continued"])) { (1i32, child) => { items = child; continue; }, _ => items };
    return selected[0];
  }
  return "wrong";
}`, "continued\n"},
	{"tuple-match-wide-binder", `function pilot(): string {
  return match ((["wide"], 2147483648i64)) { (items, n) => pass(items, n) };
}
function pass(items: string[], n: i64): string { return items[0]; }`, "wide\n"},
	{"tuple-match-negative", `function pilot(): string {
  return match ((-2147483648i32, true)) { (-2147483648i32, _) => "minimum", _ => "wrong" };
}`, "minimum\n"},
}

func TestTupleMatchPreservesProjectionContracts(t *testing.T) {
	decl, info := checkedFunc(t, `function pilot(pair: ((string[], u64), boolean)): string[] {
  return match (pair) { ((items, _), _) => items };
}`)
	f, err := BuildFunc(decl, info)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := ownershipEffects(f)
	if err != nil {
		t.Fatal(err)
	}
	parent := f.graph.Params[0]
	projections := 0
	for _, block := range f.graph.Blocks {
		if block != f.graph.Blocks[0] && len(block.Preds) == 0 {
			t.Fatal("irrefutable arm invented a disconnected failure edge")
		}
		for _, op := range block.Ops {
			if op.Kind != ssa.OpTupleGet {
				continue
			}
			effect := effects.ops[op]
			fieldType := f.values[parent.ID].typ.(ast.TupleType).Elems[0]
			if op.Args[0] != parent || op.Imm != 0 || !ast.Equal(f.values[op.Result.ID].typ, fieldType) ||
				effect.result != resultProjection || effect.parent != parent || effect.parent == op.Result {
				t.Fatal("tuple pattern lost the typed child/container relationship")
			}
			parent = op.Result
			projections++
		}
	}
	if projections != 2 {
		t.Fatalf("got %d projections, want 2", projections)
	}
}

func TestTupleMatchRejectsStaleContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*ast.MatchExpr)
		want string
	}{
		{"missing-types", func(m *ast.MatchExpr) { m.Arms[0].BindingTypes = nil }, "shape"},
		{"wrong-type", func(m *ast.MatchExpr) { m.Arms[0].BindingTypes[0] = ast.StringType{} }, "semantic field type"},
		{"nested-type", func(m *ast.MatchExpr) { m.Arms[0].TupleElems[0].NestedTypes[0] = ast.BoolType{} }, "semantic field type"},
		{"unresolved-type", func(m *ast.MatchExpr) {
			m.Arms[0].TupleElems[0].NestedTypes[1] = ast.NumberType{Width: 64, Signed: true, Polymorphic: true}
		}, "semantic field type"},
		{"nested-arity", func(m *ast.MatchExpr) { m.Arms[0].TupleElems[0].Nested = nil }, "element form"},
		{"ambiguous-element", func(m *ast.MatchExpr) { m.Arms[0].TupleElems[1].Name = "extra" }, "element form"},
		{"unsupported-range", func(m *ast.MatchExpr) { m.Arms[0].TupleElems[1].RangeHi = &ast.NumberLit{Value: 2} }, "tuple element contract"},
		{"untyped-nested-at", func(m *ast.MatchExpr) { m.Arms[0].TupleElems[0].AtBinding = "whole" }, "tuple element contract"},
		{"refutable-end", func(m *ast.MatchExpr) { m.Arms[0].Guard = &ast.BoolLit{Value: true} }, "final unguarded"},
		{"unreachable-arm", func(m *ast.MatchExpr) { m.Arms = append(m.Arms, m.Arms[0]) }, "after an irrefutable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decl, info := checkedFunc(t, `function pilot(pair: ((string[], i64), boolean)): string[] {
  return match (pair) { ((items, _), _) => items };
}`)
			m := decl.Body.Stmts[0].(*ast.Return).Value.(*ast.MatchExpr)
			tc.edit(m)
			_, err := BuildFunc(decl, info)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTupleMatchTerminatedGuardHasNoFallback(t *testing.T) {
	for _, tc := range tupleMatchCases {
		if tc.name != "tuple-match-guard-no-continuation" {
			continue
		}
		prog, info := checkedProgram(t, tc.source)
		p, err := BuildProgram(prog, info)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range p.funcs {
			for _, block := range f.graph.Blocks {
				if block != f.graph.Blocks[0] && len(block.Preds) == 0 {
					t.Fatal("terminating guard left a disconnected continuation")
				}
				for _, op := range block.Ops {
					if op.Kind == ssa.OpConstString && strings.HasPrefix(op.Str, "dead ") {
						t.Fatal("terminating guard emitted a dead body or fallback")
					}
				}
			}
		}
	}
}
