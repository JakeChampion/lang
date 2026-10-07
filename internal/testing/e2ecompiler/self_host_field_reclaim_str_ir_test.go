package e2ecompiler

import (
	"os/exec"
	"testing"
)

// TestSelfHostFieldReclaimStrIRX86_64 pins the replaced-STRING-field reclaim
// (#4355): a struct threaded through `s = step(s)` rebinds releases the
// superseded value's string field as well as its array fields, so the bump
// stays flat (98 = leaked). A field carried over by a functional update, the
// caller's original passed as a parameter, and a `let t = s.name` alias all
// stay readable across the rebinds (97 / 88), and no release underflows (99).
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

	// Consume-rebind churn: each step() replaces both the array and string
	// fields with fresh values — the superseded box's string must recycle, so
	// the bump stays flat across the second churn.
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

	// CARRIED field (functional update): `S { ...s, n: v }` keeps the string
	// pointer — the cow guard must skip it, the final value stays readable,
	// and nothing double-frees across the rebind chain.
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

	// ALIASED read: `let t = s.name` must hold its own count on the string box,
	// so t stays readable after the rebind frees the replaced field, at
	// detector zero.
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

	// SNAPSHOT param: a struct param threaded through a consume-rebind
	// (`s = s.grow()` shape via a free fn) — the caller's ORIGINAL box and
	// its string field must survive (snap guard), values stay right.
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

	// ESCAPING read: `readit(s.name)` passes the field as a call arg. Every
	// read must stay valid across rebinds, detector zero.
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

	// STRING-ONLY struct (#4355): no rc-array field, so the string is the box's
	// only reclaimable field, and the rebind churn must still stay flat.
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

	// STRING-ONLY struct, carried + aliased-read safety: the functional
	// update carries the pointer (cow-skip), and a `let t = b.name` read is
	// retained — both stay valid across rebinds, detector zero.
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

	// An i32 `.to_string()` as the replaced field's producer (the exclusion
	// note on the issue): the string is boxed at an alloc boundary, so the
	// replaced field frees cleanly — churn flat.
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
