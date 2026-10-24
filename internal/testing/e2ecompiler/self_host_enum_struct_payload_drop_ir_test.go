package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnumStructPayloadDropIRX86_64(t *testing.T) {
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

	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
enum Box { Full(Inner), Empty }
@noinline function make(): Box {
    return Full(Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) });
}
@noinline function eat(b: Box): i32 {
    match (b) {
        Full(_) => { return 5; },
        Empty => { return 0; },
    }
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 40000000) { s = eat(make()); f = f + 1; }
    return s - 5;
}`, "enum_struct_payload_churn", 0)

	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
enum Box { Full(Inner), Empty }
@noinline function make(): Box {
    return Full(Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) });
}
@noinline function f(): i32 {
    let b: Box = make();
    let r: i32 = 0;
    match (b) {
        Full(inner) => { r = inner.items[0] + inner.items[15]; },
        Empty => { r = 0; },
    }
    return r;
}
function main(): i32 { return f(); }`, "enum_struct_payload_value", 17)
}
