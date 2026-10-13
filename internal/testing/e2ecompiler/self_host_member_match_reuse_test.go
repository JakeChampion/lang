package e2ecompiler

import (
	"path/filepath"
	"testing"
)

// memberMatchReuseCases pin the consuming match over a struct-union: an arm
// that reads the fields of a member binding and builds the same member, on a
// scrutinee this frame owns and reads no further, rebuilds the value in the
// scrutinee's box and moves its reference fields out rather than retaining
// them.
//
// A member binding is the scrutinee narrowed to its record, so its field reads
// used to be reads of a second value, one holding no unit of its own: the
// ownership walk saw a box read through an alias and could neither take its
// fields nor pair its death with the construction, and every rebuild of a
// struct-union tree allocated a fresh node and released the old one. Now a
// field read of the binding is a payload read of the scrutinee, the shape the
// declared-enum match already has, and the planner's pairing, kept fields and
// payload takes all apply.
//
// Each case is oracle-checked against the interpreter under FERN_STRICT_IR, and
// `maxAllocs` is the census after the change; the rebuild cases exceed it by
// one allocation per node per round before.
var memberMatchReuseCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
	// The headline: a tree of two member kinds rebuilt in place. Building the
	// 63-node tree allocates 63 boxes; four owned rebuilds allocate none.
	{"owned-tree-rebuilt-in-place", `struct Leaf { v: i32 }
struct Node { left: Tree, right: Tree, tag: i32 }
type Tree = Leaf | Node;
function build(d: i32): Tree {
    if (d == 0) { return Leaf { v: 1 }; }
    return Node { left: build(d - 1), right: build(d - 1), tag: d };
}
@noinline
function bump(own t: Tree): Tree {
    match (t) {
        Leaf(l) => { return Leaf { ...l, v: l.v + 1 }; },
        Node(n) => { return Node { ...n, left: bump(n.left), right: bump(n.right) }; }
    }
}
function sum(t: Tree): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return sum(n.left) + sum(n.right); }
    }
}
function main(): i32 {
    let t: Tree = build(5);
    let r: i32 = 0;
    while (r < 4) { t = bump(t); r = r + 1; }
    if (sum(t) != 5 * 32) { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 70},
	// A SHARED scrutinee is not rebuilt in place: a second binding still names
	// the old tree and reads it back unchanged, so the rebuild allocates and
	// the token path degrades to a fresh box. The sum of the kept tree is the
	// assertion.
	{"shared-tree-left-intact", `struct Leaf { v: i32 }
struct Node { left: Tree, right: Tree, tag: i32 }
type Tree = Leaf | Node;
function build(d: i32): Tree {
    if (d == 0) { return Leaf { v: 1 }; }
    return Node { left: build(d - 1), right: build(d - 1), tag: d };
}
@noinline
function bump(own t: Tree): Tree {
    match (t) {
        Leaf(l) => { return Leaf { ...l, v: l.v + 1 }; },
        Node(n) => { return Node { ...n, left: bump(n.left), right: bump(n.right) }; }
    }
}
function sum(t: Tree): i32 {
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return sum(n.left) + sum(n.right); }
    }
}
function main(): i32 {
    let t: Tree = build(4);
    let keep: Tree = t;
    t = bump(t);
    if (sum(keep) != 16) { return 1; }
    if (sum(t) != 32) { return 2; }
    t = bump(t);
    if (sum(keep) != 16) { return 3; }
    if (sum(t) != 48) { return 4; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 70},
	// A reference field of the member is TAKEN out of an owned scrutinee: the
	// append then grows the buffer in place instead of copying it, so thirty
	// appends cost the doublings alone.
	{"member-buffer-grown-in-place", `struct Call { args: i32[], n: i32 }
struct Lit { v: i32 }
type E = Call | Lit;
@noinline
function push(own e: E, x: i32): E {
    match (e) {
        Call(c) => { return Call { ...c, args: c.args.append(x) }; },
        Lit(l) => { return Lit { v: l.v + x }; }
    }
}
function main(): i32 {
    let e: E = Call { args: [], n: 7 };
    let i: i32 = 0;
    while (i < 30) { e = push(e, i); i = i + 1; }
    match (e) {
        Call(c) => { if (c.args.len() != 30 || c.args[29] != 29 || c.n != 7) { return 1; } },
        Lit(_) => { return 2; }
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 12},
	// A BORROWED scrutinee's field is not taken: the caller reads the original
	// back after the call, so the append copies as a borrow always did.
	{"borrowed-member-not-taken", `struct Call { args: i32[], n: i32 }
struct Lit { v: i32 }
type E = Call | Lit;
@noinline
function pushed(e: E, x: i32): E {
    match (e) {
        Call(c) => { return Call { ...c, args: c.args.append(x) }; },
        Lit(l) => { return Lit { v: l.v + x }; }
    }
}
function main(): i32 {
    let e: E = Call { args: [1, 2], n: 7 };
    let f: E = pushed(e, 3);
    match (e) { Call(c) => { if (c.args.len() != 2) { return 1; } }, Lit(_) => { return 2; } }
    match (f) { Call(c) => { if (c.args.len() != 3 || c.args[2] != 3) { return 3; } }, Lit(_) => { return 4; } }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 6},
}

// TestSelfHostMemberMatchReuse runs each case through the self-host CLI on
// every target with the leak census on, against the interpreter oracle.
func TestSelfHostMemberMatchReuse(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	for _, tc := range memberMatchReuseCases {
		t.Run(tc.name, func(t *testing.T) {
			if want := interpExit(t, interpBin, tc.src); want != 0 {
				t.Fatalf("interpreter = %d, want 0: the case does not hold on the oracle", want)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					exit, stderr := selfHostCLIRun(t, fernBin, stdlibRoot, tc.src, target)
					if exit != 0 {
						t.Fatalf("exit = %d, want 0 (interp oracle)\n%s", exit, stderr)
					}
					censusWithin(t, tc.name, stderr, tc.maxAllocs)
				})
			}
		})
	}
}
