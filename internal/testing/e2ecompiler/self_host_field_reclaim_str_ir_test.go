package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostFieldReclaimStrIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := []byte(l.emit(t, prog))
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
			t.Errorf("%s exited %d, want %d (98 = string field leaked; 99 = over-release; 97 = value corrupted; 88 = aliased read freed under reader)", name, code, want)
		}
	}

	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n, s.n + 1], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    let s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    let i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { s = S { xs: [1, 2], name: "a" + "b", n: 0 }; let k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-flat", 0)

	run(t, `struct S { xs: i32[], name: string, n: i32 }
function bump(s: S): S { return S { ...s, n: s.n + 1 }; }
function main(): i32 {
    let s: S = S { xs: [1, 2], name: "ab" + "cd", n: 0 };
    let i: i32 = 0;
    while (i < 2000) { s = bump(s); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (s.name.len() != 4) { return 97; }
    if (s.n != 2000) { return 96; }
    return 0;
}`, "field-reclaim-str-carried-safe", 0)

	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let s: S = S { xs: [1], name: "a" + "b", n: 0 };
        let t: string = s.name;
        s = step(s);
        s = step(s);
        if (t.len() != 2) { bad = 1; }
        if (s.name.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "field-reclaim-str-aliased-read-safe", 0)

	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n, s.n + 1], name: s.name + "x", n: s.n + 1 }; }
function work(s: S): i32 { s = step(s); s = step(s); return s.name.len(); }
function main(): i32 {
    let s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        if (work(s) != 4) { bad = 1; }
        if (s.name.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return bad;
}`, "field-reclaim-str-snap-safe", 0)

	run(t, `struct S { xs: i32[], name: string, n: i32 }
function readit(nm: string): i32 { return nm.len(); }
function step(s: S): S { return S { xs: [s.n], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let s: S = S { xs: [1], name: "a" + "b", n: 0 };
        if (readit(s.name) != 2) { bad = 1; }
        s = step(s);
        s = step(s);
        if (readit(s.name) != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "field-reclaim-str-escaping-read-excluded", 0)

	run(t, `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: b.name + "x", n: b.n + 1 }; }
function main(): i32 {
    let b: B = B { name: "a" + "b", n: 0 };
    let i: i32 = 0;
    while (i < 200) { b = step(b); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { b = B { name: "a" + "b", n: 0 }; let k: i32 = 0; while (k < 3) { b = step(b); k = k + 1; } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (b.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-only-flat", 0)

	run(t, `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: b.name + "x", n: b.n + 1 }; }
function bump(b: B): B { return B { ...b, n: b.n + 1 }; }
function main(): i32 {
    let bad: i32 = 0;
    let i: i32 = 0;
    while (i < 1000) {
        let b: B = B { name: "a" + "b", n: 0 };
        let t: string = b.name;
        b = step(b);
        b = bump(b);
        if (t.len() != 2) { bad = 1; }
        if (b.name.len() != 3) { bad = 1; }
        if (b.n != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "field-reclaim-str-only-aliased-carried-safe", 0)

	run(t, `import "std/i32";
struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: s.n.to_string(), n: s.n + 1 }; }
function main(): i32 {
    let s: S = S { xs: [1], name: (7).to_string(), n: 0 };
    let i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let j: i32 = 0;
    while (j < 2000) { s = S { xs: [1], name: (7).to_string(), n: 0 }; let k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow_count() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-i32tostring-flat", 0)
}
