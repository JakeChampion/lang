package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func stringFieldLoopLocalFreshSrc(n string) string {
	return `struct S { x: i32, name: string }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let pre: string = "n";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: S = S { x: i, name: pre + "x" }; acc = acc + t.x + t.name.len(); i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stringAndArrayFieldLoopLocalSrc(n string) string {
	return `struct S { x: i32, xs: i32[], name: string }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let pre: string = "n";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < ` + n + `) { let t: S = S { x: i, xs: [i, i + 1], name: pre + "x" }; acc = acc + t.x + t.xs[0] + t.name.len(); i = i + 1; }
    if (acc < 0) { return 5; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func stringFieldLoopLocalRetainedSrc(n string) string {
	return `struct S { x: i32, name: string }
function main(): i32 {
    let before: i32 = (__heap_bump_bytes() as i32);
    let pre: string = "n";
    let keep: S[] = [];
    let i: i32 = 0;
    while (i < ` + n + `) { let t: S = S { x: i, name: pre + "x" }; keep = keep.append(t); i = i + 1; }
    if (keep.len() != ` + n + `) { return 9; }
    if ((__heap_bump_bytes() as i32) > before) { return 1; }
    return 0;
}`
}

const stringFieldLoopLocalDetectorSrc = `struct S { x: i32, name: string }
function main(): i32 {
    let pre: string = "ab";
    let i: i32 = 0; let acc: i32 = 0;
    while (i < 200) { let t: S = S { x: i, name: pre + "cd" }; acc = acc + t.x + t.name.len(); i = i + 1; }
    if (acc != 20700) { return 99; }
    return __rc_underflow_count();
}`

func TestSelfHostStringFieldLoopLocalReclaimIRX86_64(t *testing.T) {
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
		{"fresh-string-field", stringFieldLoopLocalFreshSrc},
		{"fresh-string-and-array-field", stringAndArrayFieldLoopLocalSrc},
	}
	for _, sh := range shapes {
		t.Run("fixpoint-bounded/"+sh.name, func(t *testing.T) {
			small := run(t, sh.name+"-50", sh.src("50"))
			large := run(t, sh.name+"-5000", sh.src("5000"))
			if small != large {
				t.Errorf("struct-with-fresh-string loop-local (%s) bump must be bounded: N=50 -> %d, N=5000 -> %d (string box leaked per iteration)", sh.name, small, large)
			}
		})
	}

	t.Run("fixpoint-bounded/retained-control-grows", func(t *testing.T) {
		if got := run(t, "stringfield-retained-5000", stringFieldLoopLocalRetainedSrc("5000")); got != 1 {
			t.Errorf("retaining 5000 structs must move the heap bump, got %d (9=length mismatch, 0=bump did not move, so the bounded cases above prove nothing)", got)
		}
	})

	t.Run("no-over-release", func(t *testing.T) {
		if code := run(t, "stringfield-loop-detector", stringFieldLoopLocalDetectorSrc); code != 0 {
			t.Errorf("fresh-string-field loop-local deep reclaim over-released (exit %d, 99=value mismatch, >0=__rc_underflow_count)", code)
		}
	})
}
