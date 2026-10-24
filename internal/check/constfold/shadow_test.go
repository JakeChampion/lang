package constfold

import (
	"strings"
	"testing"
)

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
			src:  "function main(): i32 { let N: i32 = 7; return N; }\n",
		},
		{
			name: "assignment-target",
			src:  "function main(): i32 { let N: i32 = 7; N = 9; return N; }\n",
		},
		{
			name: "match-binding",
			src: "enum E { A(i32), B }\n" +
				"function main(): i32 {\n    let e: E = E.A(7);\n" +
				"    match (e) {\n        A(N) => { return N; },\n        B => { return 0; },\n    }\n}\n",
		},
		{
			name: "tuple-match-binding",
			src: "function main(): i32 {\n    let t: (i32, i32) = (7, 1);\n" +
				"    match (t) {\n        (N, b) => { return N; }\n    }\n}\n",
		},
		{
			name: "nested-tuple-match-binding",
			src: "function main(): i32 {\n    let t: (i32, (i32, i32)) = (1, (7, 2));\n" +
				"    match (t) {\n        (a, (N, c)) => { return N; }\n    }\n}\n",
		},
		{
			name: "payload-sub-pattern-binding",
			src: "enum I { S(i32), Z }\nenum O { H(I), E }\n" +
				"function main(): i32 {\n    let o: O = H(S(7));\n" +
				"    match (o) {\n        H(S(N)) => { return N; },\n" +
				"        H(Z()) => { return 0; },\n        E => { return 0; },\n    }\n}\n",
		},
		{
			name: "at-binding",
			src: "enum E { A(i32), B }\nfunction take(e: E): i32 { match (e) { A(v) => { return v; }, B => { return 0; } } }\n" +
				"function main(): i32 {\n    let e: E = E.A(7);\n" +
				"    match (e) {\n        N @ A(v) => { return take(N); },\n        B => { return 0; },\n    }\n}\n",
		},
		{
			name: "match-expr-tuple-binding",
			src: "function main(): i32 {\n    let t: (i32, i32) = (7, 1);\n" +
				"    return match (t) {\n        (N, b) => N\n    };\n}\n",
		},
		{
			name: "for-each-var",
			src:  "function main(): i32 { let t: i32 = 0; for N in [1, 2] { t = t + N; } return t; }\n",
		},
		{
			name: "lambda-param",
			src:  "function main(): i32 { let f: (i32) => i32 = (N: i32) => N; return f(7); }\n",
		},
		{
			name: "destructure",
			src:  "function main(): i32 { let (N, b) = (7, 1); return N + b; }\n",
		},
		{
			name: "nested-destructure",
			src:  "function main(): i32 { let t: ((i32, i32), i32) = ((7, 1), 2); let ((a, N), c) = t; return N + c; }\n",
		},
		{
			name: "for-each-pattern",
			src:  "function main(): i32 { let xs: (i32, i32)[] = [(7, 1)]; let t: i32 = 0; for (N, v) in xs { t = t + N; } return t; }\n",
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
			src:  "function main(): i32 {\n    { let N: i32 = 1; }\n    return N;\n}\n",
		},
		{
			name: "shadow-ends-with-the-function",
			src: "function f(N: i32): i32 { return 0; }\n" +
				"function main(): i32 { return N; }\n",
		},
		{
			name: "var-init-reads-the-const-before-binding",
			src:  "function main(): i32 { let N: i32 = N + 1; return N; }\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := fold(t, "const N: i32 = 5;\n"+tc.src)
			got := countIdents(prog, "N")
			want := 0
			if tc.name == "var-init-reads-the-const-before-binding" {
				want = 1
			}
			if got != want {
				t.Errorf("countIdents = %d, want %d — the const reference did not fold;\nsource:\n%s", got, want, tc.src)
			}
		})
	}
}

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

func TestAssignToShadowedNameIsFine(t *testing.T) {
	for _, src := range []string{
		"function main(): i32 { let N: i32 = 7; N = 9; return N; }\n",
		"function main(): i32 { let xs: (i32, i32)[] = [(7, 1)]; let t: i32 = 0; for (N, v) in xs { N = 9; t = t + N; } return t; }\n",
	} {
		prog := fold(t, "const N: i32 = 5;\n"+src)
		if n := countIdents(prog, "N"); n == 0 {
			t.Errorf("the local's references were substituted with the const's value;\nsource:\n%s", src)
		}
	}
}
