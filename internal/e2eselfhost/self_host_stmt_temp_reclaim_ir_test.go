package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Statement-temporary reclamation, stage (a), on the self-host IR path: a
// discarded bare-ExprStmt whose value is a FRESH scalar-element array literal
// (`[i, i + 1, i + 2];`) is DEC'd at the statement boundary (the rc-guarded
// __fern_rc_dec, discardable_scalar_arr_lit) instead of leaking its buffer
// every iteration. This is the self-host sibling of native's
// emitOwnedTempStackDrop (internal/e2e/rc_heap_bump_stmt_temp_test.go);
// #4365 flagged it as a native-tested behavior with no self-host equivalent.
//
// Two assertions, both through the self-host x86-64 IR driver (asm_run):
//   - FIXPOINT: the discarded-temp loop's bump-growth is now BOUNDED — equal at
//     N=50 and N=5000 (before the reclaim it scaled with N: 96 -> 128 -> …).
//   - OVER-RELEASE: the discarded temp must reclaim its OWN box without touching
//     the live `xs` built from the same loop-variable operands — a wrong "owned"
//     verdict that freed a shared buffer would corrupt the sum (999) or trip the
//     __rc_underflow detector (> 0).

func stmtTempArrBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    while (i < ` + n + `) { [i, i + 1, i + 2]; i = i + 1; }
    return __heap_bump_bytes() - before;
}`
}

// A discarded owned array temp reclaims its box while the live `xs` (built from
// the same operands) is untouched: sum over i=0..199 of (i)+(i+1)+(i+2) =
// 3*(199*200/2) + 3*200 = 60300. __rc_underflow() (the self-host detector) then
// reports 0 only if nothing was over-released.
const stmtTempReclaimDetectorSrc = `function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        [i, i + 1, i + 2];
        var xs: i32[] = [i, i + 1, i + 2];
        acc = acc + xs[0] + xs[1] + xs[2];
        i = i + 1;
    }
    if (acc != 60300) { return 999; }
    return __rc_underflow();
}`

// TestSelfHostStmtTempReclaimIRX86_64 builds the self-host x86-64 IR driver once
// and drives the two programs through it.
func TestSelfHostStmtTempReclaimIRX86_64(t *testing.T) {
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
		small := run(t, "stmt-temp-50", stmtTempArrBumpSrc("50"))
		large := run(t, "stmt-temp-5000", stmtTempArrBumpSrc("5000"))
		if small != large {
			t.Errorf("discarded-array-temp bump must be bounded: N=50 -> %d, N=5000 -> %d", small, large)
		}
		if small == 0 {
			t.Errorf("expected a non-zero bounded high-water, got 0 (nothing allocated / measured)")
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "stmt-temp-detector", stmtTempReclaimDetectorSrc); code != 0 {
			t.Errorf("discarded-array-temp reclaim: exit=%d (999=value mismatch, >0=over-release)", code)
		}
	})
}
