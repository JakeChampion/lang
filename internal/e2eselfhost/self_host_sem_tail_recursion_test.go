package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostTailRecursionSource drives self-tail recursion across the parameter
// modes the rewritten loop has to carry: a borrowed string the caller keeps
// using afterwards, an owned array replaced every round, and both together
// with the array growing. The depths are past any stack here, so a body that
// did not become a loop dies rather than answers.
//
// The program reports the over-release counter and returns it, so a loop that
// released a borrowed argument fails on the exit code rather than on whether
// a freed string happened to still read correctly.
//
// No trailing escape on the print: `print` ends the line itself, and adding
// one makes the answer carry a blank line the expectation does not.
const selfHostTailRecursionSource = `import "std/i32";

function borrowed(s: string, i: i32): i32 {
    if (i == 0) { return s.len(); }
    return borrowed(s, i - 1);
}

function rebuilt(xs: i32[], i: i32): i32 {
    if (i == 0) { return xs.len(); }
    return rebuilt([i, i + 1, i + 2], i - 1);
}

function both(s: string, xs: i32[], i: i32): i32 {
    if (i == 0) { return s.len() + xs.len(); }
    return both(s, xs.append(i), i - 1);
}

// A VOID loop, which the language spells as a statement call and a fall-off
// return: returning the call is E002. Its self call is the last thing in the
// ENTRY block, with no branch before it, so the block being split is also the
// block that jumps back to the header.
function serve(c: Cell[i32]): void {
    if (c.get() >= 300000) { return; }
    c.set(c.get() + 1);
    serve(c);
}

// The tail call's argument is a VIEW of the parameter it replaces. A call
// keeps the anchor alive for the callee's frame; a jump re-enters the loop
// with that anchor already dead, so this one must NOT become a loop — the
// rewrite declines it, and the answer and the heap have to survive either
// way. irlower.rc_consumed_drop_wired is this shape, and rewriting it
// corrupted the heap of every compiler built through the path.
function shrink(t: string, n: i32): i32 {
    if (t.len() <= 1) { return n; }
    return shrink(slice_unchecked(t, 0, t.len() - 1), n + 1);
}

// A str parameter — a VIEW. The loop cannot carry one: every parameter
// gains a phi, a reference-typed phi is owned, and the entry edge supplies it
// by RETAINING, which on a view is a no-op while the matching release frees
// the box. So the rewrite declines this shape, and the assertion is that the
// caller's view survives the call with the over-release counter still at
// zero. Before the decline this answered b=0 with an empty view and the
// counter at one.
function view_walk(s: str, i: i32): i32 {
    if (i == 0) { return s.len(); }
    return view_walk(s, i - 1);
}

function main(): i32 {
    var t: string = "ab" + "cde";
    var keep: i32[] = [7, 8];

    var a: i32 = borrowed(t, 400000);
    var b: i32 = rebuilt(keep, 400000);
    var c: i32 = both(t, [1], 200);

    var ticks: Cell[i32] = cell_new(0);
    serve(ticks);

    var view: i32 = shrink("abcdefghij" + "klmnopqrst", 0);

    var lent: string = "abcde" + "fghij";
    var peek: str = slice_unchecked(lent, 0, 5);
    var vw: i32 = view_walk(peek, 2000);
    // Churn the allocator, so a box the loop released would be reissued
    // before the read below.
    var churn: string[] = [];
    var ci: i32 = 0;
    while (ci < 200) { churn = churn.append("c" + ci.to_string()); ci = ci + 1; }
    var still: i32 = peek.len();

    // The lender reads its own values AFTER the loops had them.
    var alive: i32 = t.len() + keep.len() + keep[0];

    print("a=" + a.to_string() + " b=" + b.to_string() + " c=" + c.to_string()
        + " alive=" + alive.to_string()
        + " ticks=" + ticks.get().to_string()
        + " view=" + view.to_string()
        + " vw=" + vw.to_string() + " still=" + still.to_string()
        + " underflow=" + __rc_underflow_count().to_string());
    return __rc_underflow_count();
}
`

const selfHostTailRecursionWant = "0|a=5 b=3 c=206 alive=14 ticks=300000 view=19 vw=5 still=5 underflow=0\n"

// TestSelfHostSemanticTailRecursion is the reference-typed half of #9692.
//
// The AST lowering is NOT the oracle here, which is why this is a test of its
// own rather than a row in semProductionPrograms. Its TCO
// (`irlower.tco_self_tail`) matches an op-stream `call_direct f/N` immediately
// followed by `return`, and once a parameter carries a unit the frame still
// owes a release after the call returns — the emitted asm for the `rebuilt`
// shape is `bl __fn_rebuilt` then `bl __fn___fern_arr_dec` then `ret`. So the
// pair is not adjacent, the rewrite does not fire, and the AST leg dies on
// these depths. Native does the same thing for the same reason (#9794).
//
// The graph rewrite has no such limit: it runs before the unit planner, so
// the release is placed around the loop rather than after a call that is no
// longer there. That is the whole argument for rewriting the graph instead of
// re-running the op-stream pass on produced output, and these depths are what
// hold it.
func TestSelfHostSemanticTailRecursion(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfHostTailRecursionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"x86-64-linux", "x86-64-sanitize", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			got, report, leak := semCompileRun(t, gcc, runner, fernBin, stdlibRoot, src, target, true, "", "")
			if got != selfHostTailRecursionWant {
				t.Fatalf("answered %q, want %q\nreport: %s", got, selfHostTailRecursionWant, report)
			}
			if strings.Contains(report, "the AST lowering stands") {
				t.Fatalf("the module did not produce, so nothing here tested the rewrite:\n%s", report)
			}
			// An ABSOLUTE pin, not the relative one the production table
			// gives: the loop replaces its array every round, so a rewrite
			// that dropped the superseded generation on the floor would leak
			// once per round and still answer correctly. Measured at 0 on
			// arm64-darwin under FERN_LEAKCHECK, against 122024 bytes for the
			// AST leg on the same program.
			if target == "x86-64-sanitize" && leak != 0 {
				t.Fatalf("the produced loop leaked %d bytes", leak)
			}
		})
	}
}
