package e2e

import "testing"

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
function main(): i32 { let w: Wrap = mk(); return get(w.items[0]); }
`
	_, exit, _ := compileX86_64InDir(t, src, nil)
	if exit != 42 {
		t.Fatalf("inline enum variant in struct array field: got exit %d, want 42 (variant lowered without its tag)", exit)
	}
}
