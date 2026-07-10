package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostFieldReclaimStrIRX86_64 pins the #4355 replaced-STRING-field
// reclaim: the per-type __field_reclaim_<T> consume-rebind helper used to free
// replaced ARRAY fields only, so a struct threaded through `s = step(s)`
// rebinds leaked the superseded box's string field per rebind (native is flat
// on the same shape). The helper bodies now release a replaced string field
// via the rc-aware __fern_str_free under the SAME cow + snap guards as the
// array fields: a carried-over field (functional update) is pointer-equal in
// `new` and skipped; the caller's original is protected by the snap compare.
// Balance comes from the existing construction-side retains (a non-fresh
// string field value is rc_inc'd into the box; a fresh one is sole-owned),
// plus the new read-side retain for `var t = s.name` bindings (an uncounted
// field-read alias would otherwise dangle when the rebind frees the field).
func TestSelfHostFieldReclaimStrIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = string field leaked; 99 = over-release; 97 = value corrupted; 88 = aliased read freed under reader)", name, code, want)
		}
	}

	// Consume-rebind churn: each step() replaces both the array and string
	// fields with fresh values — the superseded box's string must recycle, so
	// the bump stays flat across the second churn.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n, s.n + 1], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) { s = S { xs: [1, 2], name: "a" + "b", n: 0 }; var k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
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
    var s: S = S { xs: [1, 2], name: "ab" + "cd", n: 0 };
    var i: i32 = 0;
    while (i < 2000) { s = bump(s); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (s.name.len() != 4) { return 97; }
    if (s.n != 2000) { return 96; }
    return 0;
}`, "field-reclaim-str-carried-safe", 0)

	// ALIASED read: `var t = s.name` takes an uncounted copy — the new
	// read-side retain (alias_inc on the string box) keeps t readable after
	// the rebind frees the replaced field, at detector zero.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: s.name + "x", n: s.n + 1 }; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        var s: S = S { xs: [1], name: "a" + "b", n: 0 };
        var t: string = s.name;
        s = step(s);
        s = step(s);
        if (t.len() != 2) { bad = 1; }
        if (s.name.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
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
    var s: S = S { xs: [1, 2], name: "a" + "b", n: 0 };
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 1000) {
        if (work(s) != 4) { bad = 1; }
        if (s.name.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    return bad;
}`, "field-reclaim-str-snap-safe", 0)

	// i32_to_string as the replaced field's producer (the exclusion note on
	// the issue): on the self-host IR path __fern_i32_to_string boxes at an
	// alloc boundary, so the replaced field frees cleanly — churn flat.
	run(t, `struct S { xs: i32[], name: string, n: i32 }
function step(s: S): S { return S { xs: [s.n], name: i32_to_string(s.n), n: s.n + 1 }; }
function main(): i32 {
    var s: S = S { xs: [1], name: i32_to_string(7), n: 0 };
    var i: i32 = 0;
    while (i < 200) { s = step(s); i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    var j: i32 = 0;
    while (j < 2000) { s = S { xs: [1], name: i32_to_string(7), n: 0 }; var k: i32 = 0; while (k < 3) { s = step(s); k = k + 1; } j = j + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (s.n != 3) { return 97; }
    return 0;
}`, "field-reclaim-str-i32tostring-flat", 0)
}
