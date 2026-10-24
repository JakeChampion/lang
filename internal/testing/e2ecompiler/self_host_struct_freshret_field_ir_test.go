package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructFreshRetFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d", name, code, want)
		}
	}

	run(t, `struct Inner { a: i32, b: i32 }
struct Outer { inner: Inner, tag: i32 }
function mk_inner(): Inner { return Inner { a: 1, b: 2 }; }
function mk(): i32 {
    let o: Outer = Outer { inner: mk_inner(), tag: 7 };
    return o.inner.a + o.inner.b + o.tag;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 10;
}`, "struct_freshret_field_churn", 0)

	run(t, `struct Inner { a: i32, b: i32 }
struct Outer { inner: Inner, tag: i32 }
function mk_inner(): Inner { return Inner { a: 1, b: 2 }; }
function main(): i32 {
    let o: Outer = Outer { inner: mk_inner(), tag: 7 };
    return o.inner.a + o.inner.b + o.tag;
}`, "struct_freshret_field_value", 10)
}
