package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostEnumQualifiedCtorReclaimIRX86_64(t *testing.T) {
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

	run(t, `enum Bag { Items(i32[]), None }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mk(): i32 {
    let b: Bag = Bag.Items(id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]));
    match (b) { Items(_) => {}, None => {}, }
    return 5;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 40000000) { s = mk(); f = f + 1; }
    return s - 5;
}`, "qual_ctor_literal_churn", 0)

	run(t, `enum Bag { Items(i32[]), None }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mk(): i32 {
    let xs: i32[] = id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]);
    let b: Bag = Bag.Items(xs);
    let r: i32 = xs[0] + xs[15];
    match (b) { Items(_) => {}, None => {}, }
    return r;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 20000000) { s = mk(); f = f + 1; }
    return s - 17;
}`, "qual_ctor_bareident_churn", 0)
}
