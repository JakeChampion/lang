package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostStructStrLiteralFieldReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = literal box leaked; 99 = over-release; 97/96 = value corrupted; 88 = escaped read freed under its holder)", name, code, want)
		}
	}

	run(t, `struct B { name: string, n: i32 }
function round(i: i32): i32 { let b: B = B { name: "abc", n: i }; return b.n; }
function main(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 4000) { t = t + round(j); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (t <= 0) { return 97; }
    return 0;
}`, "str-literal-field-single-bind-flat", 0)

	run(t, `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: "xy", n: b.n + 1 }; }
function main(): i32 {
    let b: B = B { name: "abc", n: 0 };
    let i: i32 = 0;
    while (i < 200) { b = step(b); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 4000) { b = step(b); j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (b.name.len() != 2) { return 97; }
    return 0;
}`, "str-literal-field-rebind-flat", 0)

	run(t, `struct B { name: string, n: i32 }
function bump(b: B): B { return B { ...b, n: b.n + 1 }; }
function main(): i32 {
    let b: B = B { name: "abcd", n: 0 };
    let i: i32 = 0;
    while (i < 2000) { b = bump(b); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (b.name.len() != 4) { return 97; }
    if (b.n != 2000) { return 96; }
    return 0;
}`, "str-literal-field-carried-safe", 0)

	run(t, `struct B { name: string, n: i32 }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let b: B = B { name: "abc", n: i };
        let t: string = b.name;
        b = B { name: "wxyz", n: i };
        if (t.len() != 3) { bad = 1; }
        if (b.name.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-aliased-read-safe", 0)

	run(t, `struct B { name: string, n: i32 }
function mk(i: i32): B { return B { name: "leaf", n: i }; }
function main(): i32 {
    let acc: string[] = [];
    let i: i32 = 0;
    while (i < 200) { let b: B = mk(i); acc = acc.append(b.name); i = i + 1; }
    let bad: i32 = 0;
    let k: i32 = 0;
    while (k < acc.len()) { if (acc[k].len() != 4) { bad = 1; } k = k + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-container-escape-safe", 0)

	run(t, `struct B { name: string, n: i32 }
function take(s: string): i32 { return s.len(); }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let b: B = B { name: "abc", n: i };
        if (take(b.name) != 3) { bad = 1; }
        b = B { name: "de", n: i + 1 };
        if (take(b.name) != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-call-escape-safe", 0)

	run(t, `struct S { xs: i32[], a: string, b: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], a: "lit", b: s.b + "x", n: s.n + 1 }; }
function main(): i32 {
    let s: S = S { xs: [1], a: "lit", b: "a" + "b", n: 0 };
    let i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { s = S { xs: [1], a: "lit", b: "a" + "b", n: 0 }; let k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.a.len() != 3) { return 97; }
    if (s.n != 3) { return 96; }
    return 0;
}`, "str-literal-field-mixed-producers-flat", 0)
}
