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
    var f = (): i32 => { return helper(); };
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

// A `dyn Trait` coercion roots the impl methods its vtable points at, so
// they survive even though no call site names them. The root has to be
// gated on the COERCION SITE being reachable: rooting every coercion the
// checker recorded — dead ones included — drags the impl method, and
// everything it calls, into a program that can never run it (#4114).
func TestShakeDropsImplMethodBehindDeadCoercion(t *testing.T) {
	src := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return only_from_hello(); } }
function only_from_hello(): i32 { return 42; }
function dead_coercion(): i32 {
    var g: dyn Greet = Loud { n: 1 };
    return g.hello();
}
function main(): i32 { return 0; }`
	names := runShake(t, src)
	if hasName(names, "dead_coercion") {
		t.Errorf("the coercing function itself survived: %v", names)
	}
	for _, gone := range []string{"__method_Loud_hello", "only_from_hello"} {
		if hasName(names, gone) {
			t.Errorf("%s survived behind an unreachable coercion: %v", gone, names)
		}
	}
}

// The gating must not cull a LIVE coercion's impl method: nothing in the
// AST calls `__method_Loud_hello`, only the vtable cell names it, so
// culling it leaves the cell pointing at a dropped symbol (link failure).
func TestShakeKeepsImplMethodBehindLiveCoercion(t *testing.T) {
	src := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return only_from_hello(); } }
function only_from_hello(): i32 { return 42; }
function main(): i32 {
    var g: dyn Greet = Loud { n: 1 };
    return g.hello();
}`
	names := runShake(t, src)
	for _, want := range []string{"__method_Loud_hello", "only_from_hello"} {
		if !hasName(names, want) {
			t.Errorf("%s was culled from under a live vtable: %v", want, names)
		}
	}
}

// Same gating for the `e as? T` downcast vtable: a downcast-only target
// (never coerced) is rooted by the downcast site, and only when that site
// is reachable.
func TestShakeDowncastRootsFollowReachability(t *testing.T) {
	tail := `
struct Quiet { n: i32 }
impl Greet for Quiet { function hello(self: Self): i32 { return 1; } }
function probe(g: dyn Greet): i32 {
    var l: Option[Loud] = g as? Loud;
    return 0;
}
`
	head := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return only_from_hello(); } }
function only_from_hello(): i32 { return 42; }
`
	dead := head + tail + `function main(): i32 { return 0; }`
	if names := runShake(t, dead); hasName(names, "__method_Loud_hello") {
		t.Errorf("downcast-only target survived behind an unreachable downcast: %v", names)
	}

	live := head + tail + `function main(): i32 {
    var q: dyn Greet = Quiet { n: 2 };
    return probe(q);
}`
	names := runShake(t, live)
	for _, want := range []string{"probe", "__method_Loud_hello", "only_from_hello"} {
		if !hasName(names, want) {
			t.Errorf("%s was culled from under a live downcast vtable: %v", want, names)
		}
	}
}
