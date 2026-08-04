package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// #4357 (tuple sibling of the struct loop-local reclaim #4733/#4735): a fresh
// tuple loop-local carrying a fresh ARRAY-literal element (`while { var t: (i32,
// i32[]) = (i, [i, i+1]); }`) leaked the array element every iteration — the
// scalar-tuple path (tuple_lit_is_fresh_scalar) only reclaims the box with a
// shallow dec, never the elements, and a non-scalar tuple wasn't collected at all.
// The fix credits such a tuple "TUPRC:" (collect_fresh_rc_tuple_names) and routes
// its loop-rebind through emit_tuple_deep_reinit_store: arr_dec each fresh array
// element (op_tuple_get k), then the box.
//
// SOUNDNESS: only ExprArray element positions of the init literal are freed. A
// bare-ident pointer element (`(i, xs)`, xs a live array local) aliases a live
// value, so it is left untouched (leak-safe) — freeing it would double-release.
//
// Gated on the self-host x86-64 IR path: FIXPOINT (bump growth equal across N) +
// OVER-RELEASE (the fresh array element is read each iteration) + an ALIAS-SAFETY
// case proving a live shared array element is not freed.

func rcTupleLoopLocalSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0; var acc: i32 = 0;
    while (i < ` + n + `) { var t: (i32, i32[]) = (i, [i, i + 1]); acc = acc + t.0 + t.1[0]; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

// per iter reads t.0 + t.1[0..2] = i + i + (i+1) + (i+2) = 4i+3; sum 0..199 = 80200.
const rcTupleLoopLocalDetectorSrc = `function main(): i32 {
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 200) { var t: (i32, i32[]) = (i, [i, i + 1, i + 2]); acc = acc + t.0 + t.1[0] + t.1[1] + t.1[2]; i = i + 1; }
    if (acc != 80200) { return 99; }
    return __rc_underflow();
}`

// A tuple whose array element is a bare IDENT (`(i, xs)`, xs live across the loop)
// must NOT be freed by the deep reclaim — that would double-release xs. sum t.0 =
// 0..99 = 4950; t.1[0] = 7 each iter -> 700. acc = 5650; __rc_underflow == 0.
const rcTupleAliasSafetySrc = `function main(): i32 {
    var xs: i32[] = [7, 8, 9];
    var i: i32 = 0; var acc: i32 = 0;
    while (i < 100) { var t: (i32, i32[]) = (i, xs); acc = acc + t.0 + t.1[0]; i = i + 1; }
    if (acc != 5650) { return 99; }
    return __rc_underflow();
}`

func TestSelfHostRcTupleLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	run := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	t.Run("fixpoint-bounded", func(t *testing.T) {
		small := run(t, "rctuple-50", rcTupleLoopLocalSrc("50"))
		large := run(t, "rctuple-5000", rcTupleLoopLocalSrc("5000"))
		if small != large {
			t.Errorf("rc-tuple loop-local bump must be bounded: N=50 -> %d, N=5000 -> %d (array element leaked per iteration)", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0")
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "rctuple-detector", rcTupleLoopLocalDetectorSrc); code != 0 {
			t.Errorf("rc-tuple loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow)", code)
		}
	})

	t.Run("alias-safety", func(t *testing.T) {
		if code := run(t, "rctuple-alias", rcTupleAliasSafetySrc); code != 0 {
			t.Errorf("rc-tuple deep reclaim freed an ALIASED array element (exit %d, 99=value mismatch, >0=__rc_underflow — xs double-released)", code)
		}
	})
}
