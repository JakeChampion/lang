package e2e

import "testing"

// TestFreelistInlineLiteralEscape is a regression repro for a free-list
// reclamation bug (ast.RcFreeEnabled == true, the default): a FRESH inline
// literal nested as `array-literal → field of a struct literal` is reclaimed
// while the enclosing struct still references it, so reading it back yields a
// corrupt value.
//
//	struct Wrap { items: Node[], n: i32 }
//	function mk(): Wrap { return Wrap { items: [Leaf { x: 42 }], n: 7 }; }
//	// reading w.items[0] sees a freed/reused block -> match falls to `_`
//
// Diagnosis (internal/ir computeFreeEligible): the StructLit/TupleLit escape
// sink (`escapeOwned`) only taints a direct *ast.Ident field source, relying on
// construction to retain non-Ident values. A field that is an array LITERAL
// containing an inline struct/enum literal slips through: the inline element is
// rc=1, isn't tainted, and its block is returned to the freelist at the
// constructor's scope exit. Binding the element to a `var` first gives it the
// alias-inc that keeps it owned (the workaround applied in
// examples/self_host/irlower.fern lift_stmt) — so this manifested as the
// lambda-lift slices silently bailing to the AST backend.
//
// Skipped until the free-list escape analysis is fixed to retain fresh literals
// nested in container-literals-in-structs. Validate any fix against the
// flip-readiness corpus (rc_freelist_test.go) plus the full e2e suite.
func TestFreelistInlineLiteralEscape(t *testing.T) {
	t.Skip("known native free-list bug: inline literal nested in array-literal-in-struct is reclaimed early (see computeFreeEligible)")

	src := `
struct Leaf { x: i32 }
type Node = Leaf | Branch;
struct Branch { y: i32 }
struct Wrap { items: Node[], n: i32 }
function get(nd: Node): i32 {
    match (nd) { Leaf(l) => { return l.x; }, _ => { return 99; } }
    return 99;
}
function mk(): Wrap { return Wrap { items: [Leaf { x: 42 }], n: 7 }; }
function main(): i32 { var w: Wrap = mk(); return get(w.items[0]); }
`
	_, exit, _ := compileX86_64InDir(t, src, nil)
	if exit != 42 {
		t.Fatalf("inline-literal-in-struct reclaimed early: got exit %d, want 42", exit)
	}
}
