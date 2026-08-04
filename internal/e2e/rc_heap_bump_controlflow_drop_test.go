package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Perceus precise drops — slice 5: control-flow-aware placement. The last use
// of an owned local may now sit INSIDE a nested if / while / for / match. The
// drop is emitted right after the whole top-level statement that contains the
// last use — by then the local is dead on EVERY path through that statement,
// so a single top-level drop + zero-slot is sound, and any early `return` on a
// path keeps the value live to its own exit sweep (the zeroed slot makes the
// post-statement drop a no-op on paths that already returned). This reclaims
// before the (often long) tail after an `if`, which is where the win is.
//
// Earlier slices conservatively bailed on ANY nested-block use; this removes
// that bail (keeping freeEligible + the alias gates + a now-any-depth
// reassignment exclusion).
//
// Element scope: the nested-use extension is gated to PRIMITIVE-element arrays
// (i32[] / f64[] / …), whose drop is a pure buffer free with no per-element rc
// to balance across the drop point. A POINTER-element array (string[] /
// struct[] / T[][] / tuple[]) with a nested last use falls back to the exit
// sweep: its deep drop dec's each element, and an element aliased OUT across an
// early drop rides the arm64 two-word heap-string reclamation path the plan
// still defers (slice 5g) — an early drop there corrupts under allocation-reuse
// pressure (the self-host driver's `var av: string[] = args()` with
// `entry = av[1]` / `root = av[2]` last-used at `av[2]` inside an `if`). The
// cfArgsAliasSrc case below pins that this self-host shape is reclaimed
// correctly (no over-release) with the pointer-element gate in place.

func cfLit(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "0"
	}
	return "[" + strings.Join(p, ", ") + "]"
}

// cfUsedInIfSrc: `big` is used ONLY inside a taken if-branch, dead after; a
// later `tail` alloc should reuse big's freed block (peak ~1 block).
func cfUsedInIfSrc() string {
	l := cfLit(100)
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var big: i32[] = ` + l + `;
    var acc: i32 = 0;
    if (before >= 0) { acc = acc + big[0] + big[99]; }
    var tail: i32[] = ` + l + `;
    acc = acc + tail[0];
    return ((__heap_bump_bytes() as i32) - before) + acc;
}`
}

// cfBothLiveSrc: the control — big + tail both live to exit (peak ~2 blocks).
func cfBothLiveSrc() string {
	l := cfLit(100)
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var big: i32[] = ` + l + `;
    var tail: i32[] = ` + l + `;
    return ((__heap_bump_bytes() as i32) - before) + big[0] + tail[0];
}`
}

// cfEarlyReturnSrc: `big` is used inside an if that can early-return on one
// path (big still live there -> its own exit sweep) and is dead after the if
// on the other (precise drop + zero). Must be value-correct + 0 over-release
// across many iterations, with a forced interleaved alloc.
const cfEarlyReturnSrc = `function helper(seed: i32): i32 {
    var big: i32[] = [seed, seed + 1, seed + 2];
    var r: i32 = 0;
    if (seed >= 0) {
        r = big[0] + big[2];
        if (r > 1000000000) { return r; }
    }
    var junk: i32[] = [9, 9, 9];
    return r + junk[0];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        acc = acc + helper(i);
        i = i + 1;
    }
    // helper(i) = (i)+(i+2)+9 = 2i+11; sum i=0..299 = 2*44850 + 3300 = 93000
    if (acc != 93000) { return 999; }
    return __rc_underflow_count();
}`

// cfLoopThenDeadSrc: `data` is read in a loop body (read-only, no reassign),
// dead after the loop -> dropped after the while. Value-correct + 0
// over-release.
const cfLoopThenDeadSrc = `function sumloop(seed: i32): i32 {
    var data: i32[] = [seed, seed + 1, seed + 2, seed + 3];
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < 4) { s = s + data[j]; j = j + 1; }
    var after: i32[] = [1, 1];
    return s + after[0];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 300) {
        acc = acc + sumloop(i);
        i = i + 1;
    }
    // sumloop(i) = (i)+(i+1)+(i+2)+(i+3) + 1 = 4i+7; sum i=0..299 = 4*44850 + 2100 = 181500
    if (acc != 181500) { return 999; }
    return __rc_underflow_count();
}`

// cfAliasedSrc: `big` is used in an if AND aliased into a struct that outlives
// the if; the post-if precise drop must only DEC (the struct keeps it).
const cfAliasedSrc = `struct Holder { items: i32[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var big: i32[] = [i, i + 1, i + 2];
        var h: Holder = Holder { items: big, n: 0 };
        if (i >= 0) { acc = acc + big[0]; }
        var junk: i32[] = [9, 9, 9];
        acc = acc + h.items[2] + junk[0];
        i = i + 1;
    }
    // big[0]=i, h.items[2]=i+2, junk=9 -> 2i+11; sum i=0..199 = 2*19900 + 2200 = 42000
    if (acc != 42000) { return 999; }
    return __rc_underflow_count();
}`

// cfArgsAliasSrc mirrors the self-host driver's main(): a pointer-element
// (string[]) local whose elements are aliased OUT (entry / root) with the
// array's last use nested in an `if`, then allocation pressure, then the
// aliases are read. The pointer-element gate keeps this on the exit-sweep path
// (no early deep drop), so the aliased element strings survive — no over-
// release, no corruption (the bug a blanket nested-drop hit on arm64). The
// 17-char literals escape SSO so the elements are real heap strings.
const cfArgsAliasSrc = `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var av: string[] = ["aaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbb", "ccccccccccccccccc"];
        var entry: string = av[0];
        var root: string = "";
        if (av.len() >= 3) { root = av[2]; }   // av's last use is nested
        var junk: string[] = ["ddddddddddddddddd", "eeeeeeeeeeeeeeeee"];
        acc = acc + entry.len() + root.len() + junk[0].len();
        i = i + 1;
    }
    // each string 17 chars: entry + root + junk[0] = 51 per iter * 200 = 10200
    if (acc != 10200) { return 999; }
    return __rc_underflow_count();
}`

func TestWASMControlFlowDrop(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if used, both := runWasm(t, cfUsedInIfSrc()), runWasm(t, cfBothLiveSrc()); used >= both {
		t.Errorf("a local used only in an if-branch should reclaim after the if: used %d should be < both-live %d", used, both)
	}
	for name, src := range map[string]string{"early-return": cfEarlyReturnSrc, "loop-then-dead": cfLoopThenDeadSrc, "aliased": cfAliasedSrc, "args-alias": cfArgsAliasSrc} {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("%s: got %d (999=value/UAF, >0=over-release)", name, got)
		}
	}
}

func TestX86_64ControlFlowDrop(t *testing.T) {
	for name, src := range map[string]string{"early-return": cfEarlyReturnSrc, "loop-then-dead": cfLoopThenDeadSrc, "aliased": cfAliasedSrc, "args-alias": cfArgsAliasSrc} {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d", name, code)
		}
	}
}

func TestArm64ControlFlowDrop(t *testing.T) {
	for name, src := range map[string]string{"early-return": cfEarlyReturnSrc, "loop-then-dead": cfLoopThenDeadSrc, "aliased": cfAliasedSrc, "args-alias": cfArgsAliasSrc} {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("%s: code=%d", name, code)
		}
	}
}
