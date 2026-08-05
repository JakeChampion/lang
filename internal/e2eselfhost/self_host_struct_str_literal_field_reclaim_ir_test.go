package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStructStrLiteralFieldReclaimIRX86_64 pins the #6127 string-LITERAL
// struct-field reclaim.
//
// `str_producer_ownership` classifies a string literal `Static`, and the
// struct-literal string-field retain inc'd every non-Owned class on the stated
// premise that a literal "is static (.rodata, not heap rc=1) — it must NOT be
// freed". Since #2649 Option A that is only true of the DATA: `const_str` emits
// `__fern_str_box` over the interned bytes, so a literal evaluates to a fresh
// rc=1 heap box on every evaluation. The retain therefore pinned that box at
// rc=2 for the life of the program while the `k_str` drop's rc-aware
// `__fern_str_free` only took it back to 1 — a leak on a PLAIN SINGLE BIND, no
// loop and no rebind involved.
//
// Freeing it is safe because `__fern_str_free` heap-range-guards the data
// pointer: the box is released and the `.rodata` bytes are left alone.
//
// The escape cases below are the load-bearing half. Soundness rests on
// `strfld_reclaim_ok_types_of`, the whole-program read scan that already gates
// the `k_str` dec — a type whose string field is read into a call argument or a
// container store is excluded, so it is neither retained nor freed. That is the
// exact hazard class that made the sibling nested-struct release a use-after-free
// (an uncounted `items.append(p.node)` alias), so it is pinned here rather than
// assumed.
func TestSelfHostStructStrLiteralFieldReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

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

	// SINGLE BIND, no rebind: the shape the leak was measured on. Each round
	// allocates the struct box plus the literal's string box; both must be
	// reclaimed, so the churn stays flat.
	run(t, `struct B { name: string, n: i32 }
function round(i: i32): i32 { var b: B = B { name: "abc", n: i }; return b.n; }
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 4000) { t = t + round(j); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (t <= 0) { return 97; }
    return 0;
}`, "str-literal-field-single-bind-flat", 0)

	// LOOP REBIND: the replaced literal box recycles through the
	// __field_reclaim_<T> string arm, which the retain used to neutralise.
	run(t, `struct B { name: string, n: i32 }
function step(b: B): B { return B { name: "xy", n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { name: "abc", n: 0 };
    var i: i32 = 0;
    while (i < 200) { b = step(b); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 4000) { b = step(b); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (b.name.len() != 2) { return 97; }
    return 0;
}`, "str-literal-field-rebind-flat", 0)

	// CARRIED field: `B { ...b, n: v }` keeps the literal's box pointer, so the
	// cow guard must skip it and the value stay readable across 2000 rebinds.
	run(t, `struct B { name: string, n: i32 }
function bump(b: B): B { return B { ...b, n: b.n + 1 }; }
function main(): i32 {
    var b: B = B { name: "abcd", n: 0 };
    var i: i32 = 0;
    while (i < 2000) { b = bump(b); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (b.name.len() != 4) { return 97; }
    if (b.n != 2000) { return 96; }
    return 0;
}`, "str-literal-field-carried-safe", 0)

	// ALIASED READ: `var t = b.name` is a direct var-init, which the scan treats
	// as safe because the read-side alias-inc retains it. It must stay readable
	// after the rebind releases the replaced literal box.
	run(t, `struct B { name: string, n: i32 }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        var b: B = B { name: "abc", n: i };
        var t: string = b.name;
        b = B { name: "wxyz", n: i };
        if (t.len() != 3) { bad = 1; }
        if (b.name.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-aliased-read-safe", 0)

	// ESCAPE — CONTAINER STORE. `acc.append(b.name)` hands the field's box to an
	// array with no counted reference. The whole-program scan must exclude B, so
	// the box is neither retained nor freed and every stored string stays
	// readable. This is the shape that made the nested-struct sibling a UAF.
	run(t, `struct B { name: string, n: i32 }
function mk(i: i32): B { return B { name: "leaf", n: i }; }
function main(): i32 {
    var acc: string[] = [];
    var i: i32 = 0;
    while (i < 200) { var b: B = mk(i); acc = acc.append(b.name); i = i + 1; }
    var bad: i32 = 0;
    var k: i32 = 0;
    while (k < acc.len()) { if (acc[k].len() != 4) { bad = 1; } k = k + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-container-escape-safe", 0)

	// ESCAPE — CALL ARGUMENT, across a rebind. Same exclusion, the other
	// escaping position.
	run(t, `struct B { name: string, n: i32 }
function take(s: string): i32 { return s.len(); }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        var b: B = B { name: "abc", n: i };
        if (take(b.name) != 3) { bad = 1; }
        b = B { name: "de", n: i + 1 };
        if (take(b.name) != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "str-literal-field-call-escape-safe", 0)

	// MIXED producers: a literal field and a concat field on the same admitted
	// type. Both are sole-owned and reclaimed, so the churn stays flat and the
	// two arms do not double-free each other's box.
	run(t, `struct S { xs: i32[], a: string, b: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], a: "lit", b: s.b + "x", n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1], a: "lit", b: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { s = S { xs: [1], a: "lit", b: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.a.len() != 3) { return 97; }
    if (s.n != 3) { return 96; }
    return 0;
}`, "str-literal-field-mixed-producers-flat", 0)
}
