package parser

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// atPatternHead is the one lookahead every irrefutable binding site asks
// (#5356), so the same pattern head parses at all of them. Before it, each
// site hand-rolled its own: a `for` header claimed only a leading `(`, and
// neither `for` nor the `let` / `var` destructure claimed the `IDENT @` head a
// destructured parameter already took.
func TestPatternHeadAcceptedAtEverySite(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"for_struct", `struct P { x: i32, y: i32 }
function f(ps: P[]): i32 { var acc = 0; for P { x, y } in ps { acc = acc + x + y; } return acc; }`},
		{"for_struct_rename", `struct P { x: i32, y: i32 }
function f(ps: P[]): i32 { var acc = 0; for P { x: a, y: b } in ps { acc = acc + a + b; } return acc; }`},
		{"for_struct_rest", `struct P { x: i32, y: i32 }
function f(ps: P[]): i32 { var acc = 0; for P { x, .. } in ps { acc = acc + x; } return acc; }`},
		{"for_at_struct", `struct P { x: i32, y: i32 }
function f(ps: P[]): i32 { var acc = 0; for w @ P { x, y } in ps { acc = acc + w.x + x + y; } return acc; }`},
		{"for_at_tuple", `function f(ts: (i32, i32)[]): i32 { var acc = 0; for w @ (a, b) in ts { acc = acc + w.0 + a + b; } return acc; }`},
		{"var_at_struct", `struct P { x: i32, y: i32 }
function f(p: P): i32 { var w @ P { x, y } = p; return w.x + x + y; }`},
		{"let_at_struct", `struct P { x: i32, y: i32 }
function f(p: P): i32 { let w @ P { x, y } = p; return w.x + x + y; }`},
		{"var_at_tuple", `function f(t: (i32, i32)): i32 { var w @ (a, b) = t; return w.0 + a + b; }`},
		{"param_at_struct", `struct P { x: i32, y: i32 }
function f(w @ P { x, y }: P): i32 { return w.x + x + y; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Errorf("parse: %v", err)
			}
		})
	}
}

// The `@` binding names the whole value beside the pattern, so it needs a slot
// the rest of the pipeline already gives that value: the destructure's holding
// local, or the foreach's element variable. Parsing it into neither would
// leave the name silently unbound.
func TestPatternHeadAtBindingGetsAHolder(t *testing.T) {
	t.Run("destructure", func(t *testing.T) {
		prog, err := Parse(`struct P { x: i32, y: i32 }
function f(p: P): i32 { var w @ P { x, y } = p; return w.x; }`)
		if err != nil {
			t.Fatal(err)
		}
		d, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Destructure)
		if !ok {
			t.Fatalf("first stmt should be *ast.Destructure; got %T", prog.Funcs[0].Body.Stmts[0])
		}
		if d.AtName != "w" {
			t.Errorf("AtName = %q, want %q", d.AtName, "w")
		}
	})
	t.Run("foreach", func(t *testing.T) {
		prog, err := Parse(`struct P { x: i32, y: i32 }
function f(ps: P[]): i32 { for w @ P { x, y } in ps { return w.x + x + y; } return 0; }`)
		if err != nil {
			t.Fatal(err)
		}
		fe, ok := prog.Funcs[0].Body.Stmts[0].(*ast.ForEach)
		if !ok {
			t.Fatalf("first stmt should be *ast.ForEach; got %T", prog.Funcs[0].Body.Stmts[0])
		}
		if fe.Var != "w" {
			t.Errorf("element var = %q, want the `@` binding %q", fe.Var, "w")
		}
	})
}

// A refutable head is deliberately NOT claimed by atPatternHead: it belongs to
// `let … else`, which has a miss branch to run. `w @ A(n)` is the case the
// unified gate has to keep out of the destructure path — with the `@` binding
// stripped it would look like one.
func TestPatternHeadLeavesRefutableToLetElse(t *testing.T) {
	if _, err := Parse(`enum E { A(i32), B }
function f(e: E): i32 { let w @ A(n) = e else { return 0; }; return n; }`); err != nil {
		t.Errorf("refutable `@` head should still parse as let-else: %v", err)
	}
	// The same head without an `else` is a parse error rather than a silently
	// irrefutable bind.
	_, err := Parse(`enum E { A(i32), B }
function f(e: E): i32 { let w @ A(n) = e; return n; }`)
	if err == nil {
		t.Error("a refutable `@` head with no else should be rejected")
	} else if !strings.Contains(err.Error(), "else") {
		t.Errorf("error = %q, want it to name the missing `else`", err.Error())
	}
}

// A C-style `for` header opens with the same `(` a tuple pattern does, so the
// gate keeps the scan that tells them apart: a parenthesised group whose
// matching `)` is followed by `in` opens a pattern, and nothing else does.
func TestPatternHeadKeepsCStyleForApart(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var s = 0; for (var i = 0; i < 3; i = i + 1) { s = s + i; } return s; }`,
		`function f(): i32 { var s = 0; for (; false; ) { s = s + 1; } return s; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("C-style for should still parse: %v\nsrc: %s", err, src)
		}
	}
}
