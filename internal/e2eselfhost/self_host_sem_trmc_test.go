package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostTrmcSource drives tail recursion modulo cons on the typed path
// (#10462): a self-call whose result is a payload of the variant the block
// returns becomes a loop that builds the chain and fills its hole. Every walk
// is 300,000 cells deep, past any stack here, so a body that stayed a
// recursion dies rather than answers.
//
// `keep` is lent to every walk and scored again at the end, so a walk that
// released cells its caller still holds answers a different `after`; `g`
// hands the walk a temporary it may consume as it goes. Most exits fill the
// hole with a node built there; `mirror`'s hands back the node it was given,
// and `append_to`'s an element of an array it borrows, so `tails` is read back
// afterwards. The answer was confirmed at 3,000 cells against `fern -interp`,
// whose own recursion is too slow at this depth, and matches the native
// compiler at 300,000 on everything but `mirror`, which native does not
// rewrite and overflows on.
const selfHostTrmcSource = `import "std/i32";

enum List { Cons(i32, List), Neg(i32, List), Nil }
enum Rev { Node(Rev, i32), End }
enum SList { SCons(string, SList), SNil }
enum Tree { Fork(Tree, Tree), Leaf(i32) }

// The canonical shape: the cons is the returned value, the recursion its tail.
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Neg(h, t) => { return Neg(h - 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}

// A bare self-tail call beside the cons: the filter, whose hole stays put.
@noinline function drop_neg(xs: List): List {
    match (xs) {
        Cons(h, t) => { if (h < 0) { return drop_neg(t); } return Cons(h, drop_neg(t)); },
        Neg(h, t) => { return drop_neg(t); },
        Nil => { return Nil; },
    }
}

// Statements before the match run each round, and a scalar parameter advances.
@noinline function take(xs: List, n: i32): List {
    let lim: i32 = n;
    if (lim <= 0) { return Nil; }
    match (xs) {
        Cons(h, t) => { return Cons(h, take(t, lim - 1)); },
        Neg(h, t) => { return Neg(h, take(t, lim - 1)); },
        Nil => { return Nil; },
    }
}

// The hole in the first payload, with the other one computed after the call.
@noinline function to_rev(xs: List): Rev {
    match (xs) {
        Cons(h, t) => { return Node(to_rev(t), h + 1); },
        Neg(h, t) => { return Node(to_rev(t), 0 - h); },
        Nil => { return End; },
    }
}

// A counted payload beside the hole.
@noinline function tag_all(xs: SList): SList {
    match (xs) {
        SCons(h, t) => { return SCons(h + "!", tag_all(t)); },
        SNil => { return SNil; },
    }
}

// A plain self-tail call whose argument is a payload read out of the parameter.
@noinline function last_of(xs: List, d: i32): i32 {
    match (xs) {
        Cons(h, t) => { return last_of(t, h); },
        Neg(h, t) => { return last_of(t, 0 - h); },
        Nil => { return d; },
    }
}

// The base case hands back an element of an array the frame only borrows: an
// array parameter is never counted, and this one is passed along whole every
// round. So the exit fill stores a unit it has to retain rather than move.
@noinline function append_to(xs: List, tails: List[]): List {
    match (xs) {
        Cons(h, t) => { return Cons(h, append_to(t, tails)); },
        Neg(h, t) => { return Neg(h, append_to(t, tails)); },
        Nil => { return tails[0]; },
    }
}

// A self-tail call whose argument is a payload read out of a cell holding a
// view is declined (ssasem.crosses_jump): the jump would re-enter the loop past
// the frame's hold on that cell, and a view's retain is a no-op. So this walk
// keeps its self-call, and runs shallow here.
enum NL { NCons(str, NL), NNil }
function count(xs: NL, acc: i32): i32 {
    match (xs) {
        NCons(h, t) => { return count(t, acc + h.len()); },
        NNil => { return acc; },
    }
}
function spans(n: i32): NL {
    let acc: NL = NNil;
    let i: i32 = 0;
    while (i < n) { acc = NCons(slice_unchecked("abcdefgh", 0, 1 + i % 3), acc); i = i + 1; }
    return acc;
}

// Two self-calls: the first stays a call, and the second, whose result is the
// payload the construction returns, is the hole. The input leans left, so it
// is the second that goes deep. A leaf is handed back as it came in.
function mirror(t: Tree): Tree {
    match (t) {
        Fork(l, r) => { return Fork(mirror(r), mirror(l)); },
        Leaf(v) => { return t; },
    }
}

function build(n: i32): List {
    let acc: List = Nil;
    let i: i32 = 0;
    while (i < n) {
        if (i % 3 == 2) { acc = Neg(i % 7, acc); } else { acc = Cons(i % 5, acc); }
        i = i + 1;
    }
    return acc;
}

function strings(n: i32): SList {
    let acc: SList = SNil;
    let i: i32 = 0;
    while (i < n) { acc = SCons("s" + (i % 10).to_string(), acc); i = i + 1; }
    return acc;
}

function score(l: List): i32 {
    let acc: i32 = 0;
    let n: i32 = 0;
    let cur: List = l;
    let go: boolean = true;
    while (go) {
        match (cur) {
            Cons(h, t) => { acc = acc + h; n = n + 1; cur = t; },
            Neg(h, t) => { acc = acc - h; n = n + 1; cur = t; },
            Nil => { go = false; },
        }
    }
    return acc * 7 + n;
}

function rev_score(r: Rev): i32 {
    let acc: i32 = 0;
    let cur: Rev = r;
    let go: boolean = true;
    while (go) { match (cur) { Node(nx, v) => { acc = acc + v; cur = nx; }, End => { go = false; } } }
    return acc;
}

function text_len(l: SList): i32 {
    let acc: i32 = 0;
    let cur: SList = l;
    let go: boolean = true;
    while (go) { match (cur) { SCons(h, t) => { acc = acc + h.len(); cur = t; }, SNil => { go = false; } } }
    return acc;
}

function leaning(n: i32): Tree {
    let acc: Tree = Leaf(0);
    let i: i32 = 0;
    while (i < n) { acc = Fork(acc, Leaf(i % 9)); i = i + 1; }
    return acc;
}

// The mirrored tree leans right: a leaf on the left of every fork.
function right_spine(t: Tree): i32 {
    let acc: i32 = 0;
    let cur: Tree = t;
    let go: boolean = true;
    while (go) {
        match (cur) {
            Fork(l, r) => { match (l) { Leaf(v) => { acc = acc + v; }, Fork(_, _) => { acc = acc + 100000; } } cur = r; },
            Leaf(v) => { acc = acc + v; go = false; },
        }
    }
    return acc;
}

function main(): i32 {
    let n: i32 = 300000;
    // Held across every call below, so a walk that freed the cells it
    // passed would answer a different score afterwards.
    let keep: List = build(n);
    let before: i32 = score(keep);
    let a: i32 = score(inc_all(keep));
    let b: i32 = score(drop_neg(keep));
    let c: i32 = score(take(keep, n - 5));
    let d: i32 = rev_score(to_rev(keep));
    let e: i32 = text_len(tag_all(strings(n)));
    let f: i32 = last_of(keep, 0 - 1);
    // A temporary input the walk may consume as it goes.
    let g: i32 = score(inc_all(build(n)));
    let m: i32 = right_spine(mirror(leaning(n)));
    let tails: List[] = [build(10)];
    let j: i32 = score(append_to(keep, tails));
    let tail_after: i32 = score(tails[0]);
    let k: i32 = count(spans(20), 0);
    let after: i32 = score(keep);
    print("before=" + before.to_string() + " a=" + a.to_string() + " b=" + b.to_string()
        + " c=" + c.to_string() + " d=" + d.to_string() + " e=" + e.to_string()
        + " f=" + f.to_string() + " g=" + g.to_string() + " m=" + m.to_string()
        + " j=" + j.to_string() + " tail=" + tail_after.to_string() + " k=" + k.to_string()
        + " after=" + after.to_string() + " underflow=" + __rc_underflow_count().to_string());
    return __rc_underflow_count();
}
`

const selfHostTrmcWant = "0|before=1000021 a=3100021 b=3000000 c=999974 d=300003 e=900000 f=0 g=3100021 m=1199991 j=1000080 tail=59 k=39 after=1000021 underflow=0\n"

// TestSelfHostSemanticTrmc runs that program on every target through the
// typed lowering and pins the answer, a clean sanitizer leg with nothing held
// at exit, and the shape: no function the rewrite takes calls itself any more.
func TestSelfHostSemanticTrmc(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostTrmcSource), 0o644); err != nil {
		t.Fatal(err)
	}

	// The call sites left in the listing: main's, plus the one self-call of
	// `mirror` that is not in tail position. A rewritten function keeps none
	// of its own.
	t.Run("shape", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "prog.s")
		cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, stdlibRoot)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("emit asm: %v\n%s", err, stderr.String())
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]int{"inc_all": 2, "drop_neg": 1, "take": 1, "to_rev": 1, "tag_all": 1, "last_of": 1, "mirror": 2, "append_to": 1, "count": 2} {
			if got := countSelfCalls(string(asm), name); got != want {
				t.Errorf("%d call sites to %s in the listing, want %d", got, name, want)
			}
		}
	})

	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, "")
			if got != selfHostTrmcWant {
				t.Fatalf("answered %q, want %q\nreport: %s", got, selfHostTrmcWant, report)
			}
			if target == "x86-64-sanitize" && leak != 0 {
				t.Fatalf("the produced loops leaked %d bytes", leak)
			}
		})
	}
}
