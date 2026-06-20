package e2e

import "testing"

// TestInlineEnumVariantInStructArrayField guards a type-directed enum-wrapping
// bug: an inline enum-variant literal that is an element of an array literal in
// a STRUCT FIELD position did not get the field's element type propagated, so it
// lowered as a bare variant struct WITHOUT its discriminant tag and read back as
// the wrong variant.
//
//	struct Wrap { items: Node[], n: i32 }
//	function mk(): Wrap { return Wrap { items: [Leaf { x: 42 }], n: 7 }; }
//	// get(mk().items[0]) saw a tagless box -> match fell to `_`
//
// Root cause (internal/checker checkExpr *ast.StructLit): the field loop called
// checkExpr(f.Value) without first calling setElemHintFor(f.Value, fieldType) —
// the hint the Var / Return / call-argument positions set so a direct array
// literal coerces (and union-wraps) its elements. maybeWrapForUnion widens a
// DIRECT variant field value but not the elements of an array field. Fixed by
// setting the element hint before checking each field value.
//
// Note: this is unconditional (not gated on ast.RcFreeEnabled) — it reproduced
// identically free-on and free-off. It is the same shape that made the
// self-host lambda-lift's `[parser.StmtReturn{...}]` (a Stmt-union array in the
// 2-field LiftStmts struct) read back corrupt; #2759's `var ns` workaround
// happens to fix it correctly by giving the element a typed binding.
func TestInlineEnumVariantInStructArrayField(t *testing.T) {
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
		t.Fatalf("inline enum variant in struct array field: got exit %d, want 42 (variant lowered without its tag)", exit)
	}
}
