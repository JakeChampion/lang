package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A self-tail call becomes a branch back to the synthetic `loop` TCO wraps the
// body in (irlower.tco_self_tail), and the `return seen` after the `if` is the
// last thing in that loop — so the loop's final block reaches the wrapper's
// `end` live and terminated.
//
// The lift appends exactly that block at the loop's `end`, and used to stay on
// it as the current block afterwards, so the function's own tail appended it a
// SECOND time. The emitters write one label per block, which made the listing
// define `.Lssa_walk_<id>` twice and gas refuse it (#9685, #9687, fixed under
// #9688): on the compiler's own sources 55 symbols at once, which is what
// TestSelfHostSSAPhysicalRCIRArm64 assembles.
//
// This is that shape as SOURCE, which is what the reproduction costs to find:
// TestSelfHostSSALiftGivesEachBlockOneID lifts the op streams directly, and
// `ssa.repeated_block_id` guards the block list from inside the compiler. Here
// the whole path runs — lower, lift, emit, assemble, execute — so a label
// written twice for a reason the block list cannot show (two spellings
// colliding in `asmcore.sanitize_label`, say) fails here and nowhere else.
//
// The recursion is deep enough that it only returns if TCO fired — 300,000
// frames overflow the stack — so a case that stops being tail-call-rewritten
// fails here instead of quietly stopping covering the duplicate label.
//
// The arm order is load-bearing: written the other way round, with the base
// case first and the self-call LAST, the body ends in the branch the rewrite
// produced — appended at the `br` and leaving the loop's `end` with nothing to
// append — and no duplicate appears at all. So the obvious spelling of this
// program covers nothing; do not "simplify" it to one.
const loopTailLabelProgram = `@noinline
function walk(n: i32, seen: i32): i32 {
    if (n > 0) { return walk(n - 1, seen + 1); }
    return seen;
}
function main(): i32 {
    if (walk(300000, 0) == 300000) { return 7; }
    return 1;
}
`

// TestSelfHostSSALoopTailBlockEmittedOnce compiles that program through the
// register path on both ISAs, asserts the listing defines every per-block label
// once (and dangles none), and assembles + runs it: the invariant is the lift's,
// so both emitters carry it and the answer has to survive the block that is no
// longer emitted twice.
//
// FERN_SEM_IR= selects the AST lowering, which is where TCO runs: the semantic
// path produces bodies without it (#9692), so the shape reaches the lift only on
// this leg. That is also why the 55 collisions came from the compiler's own
// modules — theirs are self-tail-recursive predicates the semantic path
// declines, so they fall back to the AST lowering and pick up the wrapper.
func TestSelfHostSSALoopTailBlockEmittedOnce(t *testing.T) {
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

	src := filepath.Join(t.TempDir(), "tco.fern")
	if err := os.WriteFile(src, []byte(loopTailLabelProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("x86-64", func(t *testing.T) {
		asm := emitSSAAsmASTLeg(t, fernBin, stdlibRoot, src, "x86-64-linux")
		assertNoDuplicateLocalLabels(t, "x86-64 tco asm", asm)
		assertNoDanglingLocalLabels(t, "x86-64 tco asm", asm)
		bin := buildBin(t, gcc, t.TempDir(), "tco", string(asm))
		if out, code := runBin(exec.Command(bin), ""); code != 7 || out != "" {
			t.Fatalf("x86-64 run = %q exit %d, want %q exit 7", out, code, "")
		}
	})

	t.Run("arm64", func(t *testing.T) {
		armGCC, qemu := arm64Tooling(t)
		asm := emitSSAAsmASTLeg(t, fernBin, stdlibRoot, src, "arm64-linux")
		assertNoDuplicateLocalLabels(t, "arm64 tco asm", asm)
		assertNoDanglingLocalLabels(t, "arm64 tco asm", asm)
		bin := buildBinArm64(t, armGCC, t.TempDir(), "tco", string(asm))
		if out, code := runBin(runArm64Bin(qemu, bin), ""); code != 7 || out != "" {
			t.Fatalf("arm64 run = %q exit %d, want %q exit 7", out, code, "")
		}
	})
}

// emitSSAAsmASTLeg emits `src` for `target` through the register path with the
// AST lowering (see the FERN_SEM_IR note above).
func emitSSAAsmASTLeg(t *testing.T, fernBin, stdlibRoot, src, target string) []byte {
	t.Helper()
	cmd := exec.Command(fernBin, "-backend", "ssa", "-target", target, "-emit", "asm", src, stdlibRoot)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=", "FERN_STRICT_IR=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("emit %s asm: %v\n%s", target, err, stderr.String())
	}
	if len(asm) == 0 {
		t.Fatalf("emit %s asm: empty listing", target)
	}
	// The function's own labels are what the duplicate-label assertions below
	// are about; without them they would hold over a listing that never took
	// the path (a renamed function, a program that refused before emit).
	if !strings.Contains(string(asm), ".Lssa_walk_") {
		t.Fatalf("emit %s asm: no .Lssa_walk_ label in the listing", target)
	}
	return asm
}
