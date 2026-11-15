package checker

import (
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestCallArgDeathsFieldReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"single field", "let a = b.x; consume(a);", true},
		{"different field", "let a = b.x; inspect(b.y); consume(a);", true},
		{"repeated field", "let a = b.x; inspect(b.x); consume(a);", false},
		{"whole owner", "let a = b.x; inspect(b); consume(a);", false},
		{"nested field", "let a = b.x; inspect(b.x.y); consume(a);", false},
		{"captured field", "let a = b.x; let f = (): i32 => b.x.len(); consume(a);", false},
		{"shadowed owner", "let a = b.x; { let b = Box { x: [3], y: [4] }; inspect(b.y); } consume(a);", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse("struct Box { x: i32[], y: i32[] } function f(): void { let b = Box { x: [1], y: [2] }; " + tc.body + " }")
			if err != nil {
				t.Fatal(err)
			}
			deaths := CallArgDeaths(prog.Funcs[0], nil)
			got := false
			for _, names := range deaths.Dies {
				got = got || names["a"] && !deaths.Unadmitted["a"]
			}
			if got != tc.want {
				t.Fatalf("field alias is admitted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNoReturnFuncsResolvesTheCallee(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"builtin exit", `function f(): void { exit(1); }`, true},
		{"a no-return function", `function g(): void { exit(1); } function f(): void { g(); }`, true},
		{"a desugared exit beside a parameter named exit", `function f(exit: i32): void { todo; }`, true},
		{"a parameter named exit", `function f(exit: (i32) => void): void { exit(1); }`, false},
		{"a let named exit", `function f(): void { let exit = (k: i32): void => {}; exit(1); }`, false},
		{"a let in a branch", `function f(c: boolean): void { if (c) { let exit = (k: i32): void => {}; exit(1); } else { exit(2); } }`, false},
		{"an arm binding", `enum Fn { Has((i32) => void), Nil }
function f(o: Fn): void { match (o) { Has(exit) => { exit(1); }, Nil => { exit(2); } } }`, false},
		{"a local hides a no-return function", `function g(): void { exit(1); } function f(): void { let g = (): void => {}; g(); }`, false},
		{"a module function named exit", `function exit(c: i32): i32 { return c; } function f(): void { exit(1); }`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got := NoReturnFuncs(prog)["f"]; got != tc.want {
				t.Fatalf("f never returns = %v, want %v", got, tc.want)
			}
		})
	}
}
