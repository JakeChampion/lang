package constfold

import (
	"strings"
	"testing"
)

// A const is a module-level name, so any binder of the same name shadows it.
// The substituter had no notion of scope and rewrote EVERY matching Ident
// (#8443), so a parameter, local or match binding named after a const was
// silently replaced by the const's VALUE — a wrong answer, not a type error,
// and one no differential suite could see: Fold runs before both the
// interpreter and codegen, so `-interp` produced the same wrong answer as
// every compiled backend.
//
// Each case asserts the shadowed reference SURVIVES as an Ident. Counting is
// the right assertion here rather than a returned value, because the defect
// is precisely that the Ident is gone by the time anything downstream looks.
func TestShadowedNameIsNotSubstituted(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "param",
			src:  "function f(N: i32): i32 { return N; }\nfunction main(): i32 { return f(7); }\n",
		},
		{
			name: "local-var",
			src:  "function main(): i32 { var N: i32 = 7; return N; }\n",
		},
		{
			name: "assignment-target",
			// The un-scoped walk rewrote the assignment TARGET into a
			// literal, and the IR then reported its own invariant against a
			// legal program: "assignment target *ast.NumberLit is not an
			// lvalue the parser can produce (compiler bug)".
			src: "function main(): i32 { var N: i32 = 7; N = 9; return N; }\n",
		},
		{
			name: "match-binding",
			src: "enum E { A(i32), B }\n" +
				"function main(): i32 {\n    var e: E = E.A(7);\n" +
				"    match (e) {\n        A(N) => { return N; },\n        B => { return 0; },\n    }\n}\n",
		},
		{
			name: "for-each-var",
			src:  "function main(): i32 { var t: i32 = 0; for N in [1, 2] { t = t + N; } return t; }\n",
		},
		{
			name: "lambda-param",
			src:  "function main(): i32 { var f: (i32) => i32 = (N: i32) => N; return f(7); }\n",
		},
		{
			name: "destructure",
			src:  "function main(): i32 { var (N, b) = (7, 1); return N + b; }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := fold(t, "const N: i32 = 5;\n"+tc.src)
			if n := countIdents(prog, "N"); n == 0 {
				t.Errorf("every reference to the shadowed name was substituted with the const's value;\nsource:\n%s", tc.src)
			}
		})
	}
}

// The other half of the same rule: a reference that is NOT shadowed must
// still fold. Without this the fix above could be "never substitute", which
// would pass every case in the sibling test and break the feature outright.
func TestUnshadowedNameStillSubstitutes(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "plain-reference",
			src:  "function main(): i32 { return N; }\n",
		},
		{
			name: "sibling-scope-does-not-leak",
			// The binder is in a DISJOINT block, so it must not suppress the
			// substitution in the following one. A scope stack that popped
			// nothing would get this wrong.
			src: "function main(): i32 {\n    { var N: i32 = 1; }\n    return N;\n}\n",
		},
		{
			name: "shadow-ends-with-the-function",
			// f's parameter binds N, but the frame is popped with f, so
			// main's reference still folds. f's body deliberately does NOT
			// mention N: a surviving Ident there would make the count
			// ambiguous about which function it came from.
			src: "function f(N: i32): i32 { return 0; }\n" +
				"function main(): i32 { return N; }\n",
		},
		{
			name: "var-init-reads-the-const-before-binding",
			// `var N = N + 1` reads the const on its right-hand side and
			// shadows it only afterwards, so exactly one Ident survives.
			src: "function main(): i32 { var N: i32 = N + 1; return N; }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := fold(t, "const N: i32 = 5;\n"+tc.src)
			got := countIdents(prog, "N")
			want := 0
			if tc.name == "var-init-reads-the-const-before-binding" {
				want = 1 // the `return N`, which is the local
			}
			if got != want {
				t.Errorf("countIdents = %d, want %d — the const reference did not fold;\nsource:\n%s", got, want, tc.src)
			}
		})
	}
}

// Assigning to a const is a program error, and the substituter is the only
// pass positioned to say so: Fold clears prog.Consts, so the name is gone
// before the checker runs. Substituting into the lvalue instead turned a
// plain user mistake into `assignment target *ast.NumberLit is not an lvalue
// the parser can produce (compiler bug)` (#8443).
func TestAssignToConstIsDiagnosed(t *testing.T) {
	for _, src := range []string{
		"function main(): i32 { N = 9; return 0; }\n",
		"function main(): i32 { N += 1; return 0; }\n",
	} {
		got := foldErr(t, "const N: i32 = 5;\n"+src)
		if !strings.Contains(got, "cannot assign to const N") {
			t.Errorf("fold error = %q, want it to name the const assignment;\nsource:\n%s", got, src)
		}
	}
}

// The shadowed counterpart is a legal assignment to a local and must NOT be
// diagnosed — the rule is about the const, not about the spelling.
func TestAssignToShadowedNameIsFine(t *testing.T) {
	prog := fold(t, "const N: i32 = 5;\nfunction main(): i32 { var N: i32 = 7; N = 9; return N; }\n")
	if n := countIdents(prog, "N"); n == 0 {
		t.Error("the local's references were substituted with the const's value")
	}
}
