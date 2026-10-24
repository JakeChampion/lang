package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func rcTupleLoopLocalSrc(n string) string {
	return `function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: (i32, i32[]) = (i, [i, i + 1]); acc = acc + t.0 + t.1[0]; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const rcTupleLoopLocalDetectorSrc = `function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let t: (i32, i32[]) = (i, [i, i + 1, i + 2]); acc = acc + t.0 + t.1[0] + t.1[1] + t.1[2]; i = i + 1; }
    if (acc != 80200) { return 99; }
    return __rc_underflow_count();
}`

const rcTupleAliasSafetySrc = `function main(): i32 {
    let xs: i32[] = [7, 8, 9];
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 100) { let t: (i32, i32[]) = (i, xs); acc = acc + t.0 + t.1[0]; i = i + 1; }
    if (acc != 5650) { return 99; }
    return __rc_underflow_count();
}`

func TestSelfHostRcTupleLoopLocalReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../../compiler", "drivers/asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

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
			t.Errorf("rc-tuple loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow_count)", code)
		}
	})

	t.Run("alias-safety", func(t *testing.T) {
		if code := run(t, "rctuple-alias", rcTupleAliasSafetySrc); code != 0 {
			t.Errorf("rc-tuple deep reclaim freed an ALIASED array element (exit %d, 99=value mismatch, >0=__rc_underflow_count — xs double-released)", code)
		}
	})
}
