package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func rcFieldLoopLocalLiteralSrc(n string) string {
	return `struct P { x: i32, xs: i32[] }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: P = P { x: i, xs: [i, i + 1] }; acc = acc + t.x; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func rcFieldLoopLocalCallSrc(n string) string {
	return `struct P { x: i32, xs: i32[] }
function f(v: i32): P { return P { x: v, xs: [v, v + 1] }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: P = f(i); acc = acc + t.x; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func scalarLoopLocalCallSrc(n string) string {
	return `struct P { x: i32, y: i32 }
function mk(v: i32): P { return P { x: v, y: v + 1 }; }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: P = mk(i); acc = acc + t.x + t.y; i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

const rcFieldLoopLocalDetectorSrc = `struct P { x: i32, xs: i32[] }
function main(): i32 {
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) {
        let t: P = P { x: i, xs: [i, i + 1, i + 2] };
        acc = acc + t.x + t.xs[0] + t.xs[1] + t.xs[2];
        i = i + 1;
    }
    if (acc != 80200) { return 99; }
    return __rc_underflow_count();
}`

func TestSelfHostRcFieldLoopLocalReclaimIRX86_64(t *testing.T) {
	boxedProbes(t)
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

	shapes := []struct {
		name string
		src  func(n string) string
	}{
		{"literal-source", rcFieldLoopLocalLiteralSrc},
		{"fresh-call-source", rcFieldLoopLocalCallSrc},
		{"scalar-fresh-call-source", scalarLoopLocalCallSrc},
	}
	for _, sh := range shapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("rc-field struct loop-local (%s) bump must be bounded: N=50 -> %d, N=5000 -> %d (xs buffer leaked per iteration)", sh.name, small, large)
			}
			if small == 0 {
				t.Errorf("%s: expected a non-zero bounded high-water, got 0", sh.name)
			}
		})
	}

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "rcfield-loop-detector", rcFieldLoopLocalDetectorSrc); code != 0 {
			t.Errorf("rc-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow_count)", code)
		}
	})
}
