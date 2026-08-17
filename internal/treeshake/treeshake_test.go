package treeshake_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/treeshake"
)

// runShake parses, type-checks, then runs the treeshaker.
// Returns the surviving function names in declaration order.
func runShake(t *testing.T, src string, extras ...string) []string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	treeshake.Run(prog, info, extras...)
	names := make([]string, 0, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		names = append(names, fn.Name)
	}
	return names
}

// hasName reports whether names contains s.
func hasName(names []string, s string) bool {
	for _, n := range names {
		if n == s {
			return true
		}
	}
	return false
}

// TestShakeDropsUnreferencedFunction — a function not reached
// from main (or any other entry point) goes away.
func TestShakeDropsUnreferencedFunction(t *testing.T) {
	src := `function unused(): i32 { return 1; }
function main(): i32 { return 0; }`
	names := runShake(t, src)
	if hasName(names, "unused") {
		t.Errorf("unused survived treeshake: %v", names)
	}
	if !hasName(names, "main") {
		t.Errorf("main went away: %v", names)
	}
}

// TestShakeKeepsCalledFunctions — chained calls keep every
// transitive callee alive.
func TestShakeKeepsCalledFunctions(t *testing.T) {
	src := `function leaf(): i32 { return 7; }
function middle(): i32 { return leaf(); }
function main(): i32 { return middle(); }`
	names := runShake(t, src)
	for _, want := range []string{"leaf", "middle", "main"} {
		if !hasName(names, want) {
			t.Errorf("%s should have survived: %v", want, names)
		}
	}
}

// TestShakeKeepsFunctionAddressed — when a function is taken
// by value (assigned to a var, passed as an arg) rather than
// directly called, the Ident reference in the body keeps it
// alive.
func TestShakeKeepsFunctionAddressed(t *testing.T) {
	src := `function helper(x: i32): i32 { return x + 1; }
function main(): i32 {
    var f: (i32) => i32 = helper;
    return f(41);
}`
	names := runShake(t, src)
	if !hasName(names, "helper") {
		t.Errorf("helper should survive (taken by value): %v", names)
	}
}

// TestShakeHonoursExtras — names passed in `extras` are kept
// alive even with no AST reference. Used by codegen wrappers
// that emit calls outside the AST.
func TestShakeHonoursExtras(t *testing.T) {
	src := `function survives(): i32 { return 1; }
function main(): i32 { return 0; }`
	names := runShake(t, src, "survives")
	if !hasName(names, "survives") {
		t.Errorf("`survives` was named in extras but got dropped: %v", names)
	}
}

// TestShakeIsIdempotent — running twice produces the same
// result as running once.
func TestShakeIsIdempotent(t *testing.T) {
	src := `function unused(): i32 { return 1; }
function used(): i32 { return 7; }
function main(): i32 { return used(); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	treeshake.Run(prog, info)
	afterFirst := make([]string, 0, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		afterFirst = append(afterFirst, fn.Name)
	}
	treeshake.Run(prog, info)
	afterSecond := make([]string, 0, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		afterSecond = append(afterSecond, fn.Name)
	}
	if len(afterFirst) != len(afterSecond) {
		t.Errorf("non-idempotent: first run kept %d funcs, second kept %d", len(afterFirst), len(afterSecond))
	}
	for i := range afterFirst {
		if i < len(afterSecond) && afterFirst[i] != afterSecond[i] {
			t.Errorf("non-idempotent at index %d: first=%q second=%q", i, afterFirst[i], afterSecond[i])
		}
	}
}

// TestShakeNoOpOnEmptyProgram — guard against an obvious
// panic shape; treeshake should accept a program with no
// funcs (paranoid; the rest of the pipeline rejects it
// upstream, but tree-shake is the last fail-safe).
func TestShakeNoOpOnEmptyProgram(t *testing.T) {
	prog := &ast.Program{}
	treeshake.Run(prog, nil)
	if len(prog.Funcs) != 0 {
		t.Errorf("expected 0 funcs, got %d", len(prog.Funcs))
	}
}

// TestShakeWalksLambdaBody — anonymous `function (...) { ... }`
// expressions live inline at treeshake time (closureconv hoists
// them later, after treeshake runs). Without a Lambda case in
// walkExpr, any top-level function referenced only from inside
// a lambda body — most commonly a mangled method name like
// `__method_string_trim` — got pruned, and link fired
// "undefined reference to __method_string_trim".
func TestShakeWalksLambdaBody(t *testing.T) {
	src := `function helper(): i32 { return 7; }
function main(): i32 {
    var f = function (): i32 { return helper(); };
    return f();
}`
	names := runShake(t, src)
	if !hasName(names, "helper") {
		t.Errorf("helper should survive (referenced inside lambda body): %v", names)
	}
}

// TestShakeKeepsStructUpdateBaseCall — a call reachable ONLY through the spread
// source of a struct-update literal (`S { ...mk(), … }`) is a real reference.
// The walk used to descend into the field values but not the base, so `mk` was
// pruned while the emitted literal still called it — the native assembler then
// failed with an undefined label.
func TestShakeKeepsStructUpdateBaseCall(t *testing.T) {
	src := `struct S { a: i32, b: i32 }
function mk(): S { return S { a: 1, b: 2 }; }
function main(): i32 { var s: S = S { ...mk(), b: 40 }; return s.a + s.b; }`
	names := runShake(t, src)
	if !hasName(names, "mk") {
		t.Errorf("mk is referenced by the struct-update base and must survive: %v", names)
	}
}
