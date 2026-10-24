package e2e

import (
	"testing"
)

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
    let acc: i32 = 0;
    let i: i32 = 0;
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
	if got := runWasm(t, ownTreeSrc); got != 0 {
		t.Errorf("tree inc: got %d, want 0", got)
	}
	bump := func(n string) string {
		return `enum Tree { Node(Tree, Tree, i32), Leaf }
function inc(own t: Tree): Tree { match (t) { Node(l,r,v) => { return Node(inc(l), inc(r), v+1); }, Leaf => { return Leaf; } } }
function total(t: Tree): i32 { match (t) { Node(l,r,v) => { return total(l)+total(r)+v; }, Leaf => { return 0; } } }
function build(d: i32): Tree { if (d == 0) { return Leaf; } return Node(build(d-1), build(d-1), d); }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0;
    while (i < ` + n + `) { let u: i32 = total(inc(build(5))); i = i + 1; }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	small := runWasm(t, bump("1000"))
	large := runWasm(t, bump("10000"))
	if small != large {
		t.Errorf("consuming tree traversal should be bounded: N=1000 -> %d, N=10000 -> %d", small, large)
	}
}
