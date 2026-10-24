package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructMethodTypeParamsIRX86_64(t *testing.T) {
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

	run(t, `struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function dbl(x: i32): i32 { return x * 2; }
function label(x: i32): string { if (x > 5) { return "big"; } return "small"; }
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let d: Box[i32] = b.map(dbl);
    let s: Box[string] = b.map(label);
    let lm: Box[i32] = b.map((x: i32) => x + 1);
    if (s.get() != "big") { return 90; }
    return d.get() + lm.get();
}`, "map-own-typaram", 22)

	run(t, `struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function dbl(x: i32): i32 { return x * 2; }
function shout(x: i32): string { return "n"; }
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let c: Box[string] = b.map(dbl).map(shout);
    if (c.get() != "n") { return 91; }
    return 33;
}`, "map-chained", 33)

	run(t, `struct Pair[K, V] { k: K, v: V }
function (p: Pair[K, V]) remap[W](f: (V) => W): Pair[K, W] { return Pair { k: p.k, v: f(p.v) }; }
function (p: Pair[K, V]) val(): V { return p.v; }
function neg(x: i32): i32 { return 0 - x; }
function main(): i32 {
    let p: Pair[string, i32] = Pair { k: "a", v: 5 };
    let q: Pair[string, i32] = p.remap(neg);
    return 100 + q.val();
}`, "two-receiver-vars", 95)

	run(t, `struct Box[T] { v: T }
function (b: Box[T]) tag[E](e: E): E { return e; }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 {
    let b: Box[i32] = Box { v: 3 };
    let t: i32 = b.tag(40);
    return t + b.get();
}`, "own-var-in-param-only", 43)

	run(t, `struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function (b: Box[T]) swap(x: T): Box[T] { return Box { v: x }; }
function main(): i32 {
    let b: Box[i32] = Box { v: 4 };
    let c: Box[i32] = b.swap(9);
    let s: Box[string] = Box { v: "hi" };
    if (s.get() != "hi") { return 92; }
    return b.get() + c.get();
}`, "no-own-typaram-unchanged", 13)
}
