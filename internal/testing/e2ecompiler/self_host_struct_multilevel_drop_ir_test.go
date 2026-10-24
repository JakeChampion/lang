package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructMultiLevelDropIRX86_64(t *testing.T) {
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

	run(t, `struct C { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
function mk(): i32 {
    let a: A = A { b: B { c: C { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, bt: 2 }, at: 7 };
    return a.b.c.items[0] + a.b.c.items[15] + a.b.bt + a.at;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 26;
}`, "struct_multilevel_drop_churn", 0)

	run(t, `struct C { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct B { c: C, bt: i32 }
struct A { b: B, at: i32 }
function main(): i32 {
    let a: A = A { b: B { c: C { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, bt: 2 }, at: 7 };
    let sum: i32 = 0; let j: i32 = 0;
    while (j < 16) { sum = sum + a.b.c.items[j]; j = j + 1; }
    return sum + a.b.bt + a.at;
}`, "struct_multilevel_drop_value", 145)

	run(t, `struct Z { items: i32[], zt: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct Y { z: Z, yt: i32 }
struct X { y: Y, xt: i32 }
struct W { x: X, wt: i32 }
function mk(): i32 {
    let w: W = W { x: X { y: Y { z: Z { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]), zt: 1 }, yt: 2 }, xt: 3 }, wt: 4 };
    return w.x.y.z.items[0] + w.x.y.z.items[15] + w.x.y.z.zt + w.x.y.yt + w.x.xt + w.wt;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 2000000) { s = mk(); f = f + 1; }
    return s - 27;
}`, "struct_multilevel_drop_four", 0)
}
