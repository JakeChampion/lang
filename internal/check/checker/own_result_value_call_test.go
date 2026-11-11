package checker

import (
	"strings"
	"testing"
)

// A call through a function value whose type has an `own` result gives a
// value that no other name holds. So a local that it starts is owned at its
// last use, as the local of a named call is.
func TestLocalFromOwnResultOfFunctionValueIsOwned(t *testing.T) {
	const decls = "struct A { x: i32[] }\n" +
		"function (own a: A) step(): A { return A { x: a.x.append(1) }; }\n" +
		"function bump(own a: A, n: i32): own A { return A { x: a.x.append(n) }; }\n" +
		"function plain(own a: A, n: i32): A { return A { x: a.x.append(n) }; }\n" +
		"struct P[S] { update: (own S, i32) => own S, effects: (own S) => own S }\n" +
		"struct Q[S] { update: (own S, i32) => S, effects: (own S) => S }\n"
	const main = "\nfunction main(): i32 { return 0; }\n"
	good := "function run[S](p: P[S], own s: S): S { let next = p.update(s, 3); return p.effects(next); }\n" +
		"function start(): i32 { let p: P[A] = P { update: bump, effects: (own a: A): own A => a.step() }; return run(p, A { x: [] }).x.len(); }"
	if err := checkSource(t, decls+good+main); err != nil {
		t.Errorf("own result: %v", err)
	}
	// With no `own` result, the value of the call can borrow an argument.
	bad := "function run[S](p: Q[S], own s: S): S { let next = p.update(s, 3); return p.effects(next); }"
	err := checkSource(t, decls+bad+main)
	if err == nil {
		t.Fatal("a result that is not `own`: accepted")
	}
	if !strings.Contains(err.Error(), "argument to owned parameter must be an owned value") {
		t.Errorf("want the owned parameter error, got:\n%v", err)
	}
}
