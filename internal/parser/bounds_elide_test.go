package parser

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// idxNodes returns every `arr[idx]` Index node (both operands the named idents)
// reachable from prog, in source order.
func idxNodes(t *testing.T, src, arr, idx string) []*ast.Index {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	var out []*ast.Index
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if ix, ok := n.(*ast.Index); ok {
			if a, ok := ix.Array.(*ast.Ident); ok && a.Name == arr {
				if i, ok := ix.Idx.(*ast.Ident); ok && i.Name == idx {
					out = append(out, ix)
				}
			}
		}
		return true
	})
	return out
}

// wantElided asserts exactly one matching `arr[idx]` exists and it IS marked.
func wantElided(t *testing.T, name, src string) {
	t.Helper()
	nodes := idxNodes(t, src, "a", "i")
	if len(nodes) != 1 {
		t.Fatalf("%s: expected exactly one a[i] node, got %d", name, len(nodes))
	}
	if !nodes[0].Unchecked {
		t.Errorf("%s: a[i] should be Unchecked (bounds-check elided) but was not", name)
	}
}

// wantChecked asserts exactly one matching `arr[idx]` exists and it is NOT
// marked (the conservative pass must leave the check in place).
func wantChecked(t *testing.T, name, src string) {
	t.Helper()
	nodes := idxNodes(t, src, "a", "i")
	if len(nodes) != 1 {
		t.Fatalf("%s: expected exactly one a[i] node, got %d", name, len(nodes))
	}
	if nodes[0].Unchecked {
		t.Errorf("%s: a[i] must stay checked but was marked Unchecked", name)
	}
}

func TestElideLenBounded_Eligible(t *testing.T) {
	cases := []struct{ name, src string }{
		{"while_sum", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"for_sum", `function f(a: i32[]): i32 {
			var s: i32 = 0;
			for (var i: i32 = 0; i < a.len(); i = i + 1) { s = s + a[i]; }
			return s;
		}`},
		{"rhs_read", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { s = a[i] * 2; i = i + 1; }
			return s;
		}`},
		{"step_plus_two", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { s = a[i]; i = i + 2; }
			return s;
		}`},
		{"len_free_fn", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < len(a)) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"nested_if_access", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { if (s > 0) { s = s + a[i]; } i = i + 1; }
			return s;
		}`},
	}
	for _, c := range cases {
		wantElided(t, c.name, c.src)
	}
}

func TestElideLenBounded_Ineligible(t *testing.T) {
	cases := []struct{ name, src string }{
		{"le_bound", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i <= a.len()) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"access_after_increment", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { i = i + 1; s = s + a[i]; }
			return s;
		}`},
		{"array_reassigned", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { a = a.append(0); s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"non_monotonic_decrement", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 100;
			while (i < a.len()) { s = s + a[i]; i = i - 1; }
			return s;
		}`},
		{"unknown_start", `function f(a: i32[], k: i32): i32 {
			var s: i32 = 0; var i: i32 = k;
			while (i < a.len()) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"no_start_stmt", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0; s = s + 1;
			while (i < a.len()) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"array_shadowed", `function f(a: i32[], b: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { var a: i32[] = b; s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"index_shadowed", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { var i: i32 = 999; s = s + a[i]; i = i + 1; }
			return s;
		}`},
		{"index_reassigned_nonincrement", `function f(a: i32[], k: i32): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { s = s + a[i]; i = k; }
			return s;
		}`},
		{"in_lambda_body", `function f(a: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < a.len()) { var g: () => i32 = () => a[i]; s = s + g(); i = i + 1; }
			return s;
		}`},
		{"bound_is_other_array", `function f(a: i32[], b: i32[]): i32 {
			var s: i32 = 0; var i: i32 = 0;
			while (i < b.len()) { s = s + a[i]; i = i + 1; }
			return s;
		}`},
	}
	for _, c := range cases {
		wantChecked(t, c.name, c.src)
	}
}
