package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructDeepDropIRX86_64(t *testing.T) {
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
struct Outer { inner: Inner, tag: i32 }
function mk(): i32 {
    let o: Outer = Outer { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, tag: 7 };
    return o.inner.items[0] + o.inner.items[15] + o.tag;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 24;
}`, "struct_deep_drop_churn", 0)

	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct Outer { inner: Inner, tag: i32 }
function main(): i32 {
    let o: Outer = Outer { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, tag: 7 };
    let sum: i32 = 0; let j: i32 = 0;
    while (j < 16) { sum = sum + o.inner.items[j]; j = j + 1; }
    return sum + o.tag;
}`, "struct_deep_drop_value", 143)

	run(t, `struct Node { kids: Node[], v: i32 }
@noinline function id(xs: i32[]): i32[] { return xs; }
function mk(): i32 {
    let leaf: Node = Node { kids: [], v: id([5])[0] };
    let root: Node = Node { kids: [leaf], v: 3 };
    return root.v + root.kids[0].v;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 1000000) { s = mk(); f = f + 1; }
    return s - 8;
}`, "struct_deep_drop_cyclic_safe", 0)

	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct Mid { inner: Inner, m: i32 }
struct Outer { mid: Mid, tag: i32 }
function mk(): i32 {
    let o: Outer = Outer { mid: Mid { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, m: 2 }, tag: 7 };
    return o.mid.inner.items[0] + o.mid.inner.items[15] + o.mid.m + o.tag;
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 150000000) { s = mk(); f = f + 1; }
    return s - 26;
}`, "struct_deep_drop_depth2_churn", 0)

	run(t, `struct C { name: string, xs: i32[] }
@noinline function ids(s: string): string { return s; }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct B { c: C, y: i32 }
struct A { b: B, z: i32 }
function mk(): i32 {
    let a: A = A { b: B { c: C { name: ids("abc"), xs: id([0, 1]) }, y: 9 }, z: 4 };
    return a.b.c.xs[0] + a.b.c.xs[1] + a.b.c.name.len();
}
function main(): i32 {
    let s: i32 = 0; let f: i32 = 0;
    while (f < 100000000) { s = mk(); f = f + 1; }
    return s - 4;
}`, "struct_deep_drop_depth3_str_churn", 0)

	run(t, `struct Inner { items: i32[] }
@noinline function id(xs: i32[]): i32[] { return xs; }
struct Mid { inner: Inner, m: i32 }
struct Outer { mid: Mid, tag: i32 }
function main(): i32 {
    let o: Outer = Outer { mid: Mid { inner: Inner { items: id([1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]) }, m: 2 }, tag: 7 };
    let sum: i32 = 0; let j: i32 = 0;
    while (j < 16) { sum = sum + o.mid.inner.items[j]; j = j + 1; }
    return sum + o.mid.m + o.tag;
}`, "struct_deep_drop_depth2_value", 145)
}
