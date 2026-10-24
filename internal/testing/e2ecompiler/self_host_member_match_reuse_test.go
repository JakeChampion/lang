package e2ecompiler

import (
	"path/filepath"
	"testing"
)

var memberMatchReuseCases = []struct {
	name      string
	src       string
	maxAllocs int64
}{
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
