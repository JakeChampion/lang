package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Consuming match generalises beyond lists to arbitrary recursive ADTs: a TREE
// traversal over an `own` parameter reclaims each node's cell as it rebuilds
// (each child binding is owned and consumed by the recursive call, whose own
// consuming match frees its node). Two pointer payloads per node — the
// consuming-match payload move handles both. Zero leak, bounded high-water.
// This pins that the FBIP win covers nested structures, not just `Cons` lists.

const ownTreeSrc = `enum Tree { Node(Tree, Tree, i32), Leaf }
function inc(own t: Tree): Tree {
    match (t) {
        Node(l, r, v) => { return Node(inc(l), inc(r), v + 1); },
        Leaf => { return Leaf; },
    }
}
function total(t: Tree): i32 {
    match (t) { Node(l, r, v) => { return total(l) + total(r) + v; }, Leaf => { return 0; } }
}
function build(d: i32): Tree {
    if (d == 0) { return Leaf; }
    return Node(build(d - 1), build(d - 1), d);
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        acc = acc + total(inc(build(4)));   // depth-4 full tree, +1 per node, sum = 41
        i = i + 1;
    }
    if (acc != 4100) { return 999; }
    return __rc_underflow_count();
}`

func TestX86_64OwnTreeMatch(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownTreeSrc); code != 0 {
		t.Errorf("tree inc: got %d, want 0", code)
	}
}

func TestArm64OwnTreeMatch(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownTreeSrc); code != 0 {
		t.Errorf("tree inc: got %d, want 0", code)
	}
}

func TestWASMOwnTreeMatch(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownTreeSrc); got != 0 {
		t.Errorf("tree inc: got %d, want 0", got)
	}
	// Bounded: each node cell is reclaimed (recycled into the rebuilt node), so
	// a build→inc→total loop holds a flat high-water rather than leaking a tree
	// per iteration.
	bump := func(n string) string {
		return `enum Tree { Node(Tree, Tree, i32), Leaf }
function inc(own t: Tree): Tree { match (t) { Node(l,r,v) => { return Node(inc(l), inc(r), v+1); }, Leaf => { return Leaf; } } }
function total(t: Tree): i32 { match (t) { Node(l,r,v) => { return total(l)+total(r)+v; }, Leaf => { return 0; } } }
function build(d: i32): Tree { if (d == 0) { return Leaf; } return Node(build(d-1), build(d-1), d); }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    while (i < ` + n + `) { var u: i32 = total(inc(build(5))); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("1000"))
	large := runWasm(t, bump("10000"))
	if small != large {
		t.Errorf("consuming tree traversal should be bounded: N=1000 -> %d, N=10000 -> %d", small, large)
	}
}
