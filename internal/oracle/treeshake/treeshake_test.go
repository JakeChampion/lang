package treeshake_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/oracle/treeshake"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

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

func hasName(names []string, s string) bool {
	for _, n := range names {
		if n == s {
			return true
		}
	}
	return false
}

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

func TestShakeKeepsFunctionAddressed(t *testing.T) {
	src := `function helper(x: i32): i32 { return x + 1; }
function main(): i32 {
    let f: (i32) => i32 = helper;
    return f(41);
}`
	names := runShake(t, src)
	if !hasName(names, "helper") {
		t.Errorf("helper should survive (taken by value): %v", names)
	}
}

func TestShakeHonoursExtras(t *testing.T) {
	src := `function survives(): i32 { return 1; }
function main(): i32 { return 0; }`
	names := runShake(t, src, "survives")
	if !hasName(names, "survives") {
		t.Errorf("`survives` was named in extras but got dropped: %v", names)
	}
}

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

func TestShakeNoOpOnEmptyProgram(t *testing.T) {
	prog := &ast.Program{}
	treeshake.Run(prog, nil)
	if len(prog.Funcs) != 0 {
		t.Errorf("expected 0 funcs, got %d", len(prog.Funcs))
	}
}

func TestShakeWalksLambdaBody(t *testing.T) {
	src := `function helper(): i32 { return 7; }
function main(): i32 {
    let f = (): i32 => { return helper(); };
    return f();
}`
	names := runShake(t, src)
	if !hasName(names, "helper") {
		t.Errorf("helper should survive (referenced inside lambda body): %v", names)
	}
}

func TestShakeKeepsStructUpdateBaseCall(t *testing.T) {
	src := `struct S { a: i32, b: i32 }
function mk(): S { return S { a: 1, b: 2 }; }
function main(): i32 { let s: S = S { ...mk(), b: 40 }; return s.a + s.b; }`
	names := runShake(t, src)
	if !hasName(names, "mk") {
		t.Errorf("mk is referenced by the struct-update base and must survive: %v", names)
	}
}

func TestShakeDropsImplMethodBehindDeadCoercion(t *testing.T) {
	src := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return only_from_hello(); } }
function only_from_hello(): i32 { return 42; }
function dead_coercion(): i32 {
    let g: dyn Greet = Loud { n: 1 };
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

func TestShakeKeepsImplMethodBehindLiveCoercion(t *testing.T) {
	src := `trait Greet { function hello(self: Self): i32; }
struct Loud { n: i32 }
impl Greet for Loud { function hello(self: Self): i32 { return only_from_hello(); } }
function only_from_hello(): i32 { return 42; }
function main(): i32 {
    let g: dyn Greet = Loud { n: 1 };
    return g.hello();
}`
	names := runShake(t, src)
	for _, want := range []string{"__method_Loud_hello", "only_from_hello"} {
		if !hasName(names, want) {
			t.Errorf("%s was culled from under a live vtable: %v", want, names)
		}
	}
}

func TestShakeDowncastRootsFollowReachability(t *testing.T) {
	tail := `
struct Quiet { n: i32 }
impl Greet for Quiet { function hello(self: Self): i32 { return 1; } }
function probe(g: dyn Greet): i32 {
    let l: Option[Loud] = g as? Loud;
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
    let q: dyn Greet = Quiet { n: 2 };
    return probe(q);
}`
	names := runShake(t, live)
	for _, want := range []string{"probe", "__method_Loud_hello", "only_from_hello"} {
		if !hasName(names, want) {
			t.Errorf("%s was culled from under a live downcast vtable: %v", want, names)
		}
	}
}
