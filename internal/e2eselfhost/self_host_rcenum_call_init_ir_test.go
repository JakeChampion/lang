package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcEnumCallInitIRX86_64 pins #4355 slice 5 on the self-host IR
// path — two coupled changes:
//
//  1. CALL-INIT admission: the RCENUM enum-local reclaim (loop-rebind /
//     consume deep-drop of a fresh, match-consumed enum local) only fired for
//     DIRECT variant-ctor inits (`var b = Full([..])`). A factored constructor
//     (`var e: E = mk(i)`) was never credited, so the whole chain (enum box +
//     payload struct box + its string/array fields) leaked per iteration.
//     opt_fresh_ret_fns_of now emits "RCE:<name>|<Enum>" entries for functions
//     whose every return is a fresh direct variant construction (with fresh
//     string fields in StructLit payloads), and collect_fresh_rcenum_names
//     admits a call init against that set.
//
//  2. __struct_drop_<T> RETURN-CLOBBER fix: the helper's documented contract
//     is "returns the box", but the x86 body set %rax at entry and the field-
//     release calls clobbered it — it returned the LAST-FREED FIELD pointer.
//     emit_enum_variant_drops CONSUMES that return for its payload-box free,
//     so every enum-with-struct-payload consume dec'd the last-freed field a
//     second time (one rc-underflow tick per consume — latent in the shipped
//     TestSelfHostEnumStructPayloadDropIRX86_64 shape, which only asserts
//     boundedness) while the payload box leaked; a STRING field segfaulted
//     via __fern_str_free on the wrong block. The body now reloads the box
//     from the stack arg before ret.
func TestSelfHostRcEnumCallInitIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = chain leaked; 99 = rc underflow / over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// CALL-INIT, STRING-field payload struct (the slice target): churn flat at
	// detector zero. mk's every return is a fresh direct ctor with a fresh
	// concat string field, so the RCE scan admits it and the loop-rebind
	// deep-drop reclaims enum box + S box + the string each iteration.
	run(t, `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm + "x", n: n }, n); }
function main(): i32 {
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = mk(base, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var e2: E = mk(base, j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "rcenum-call-init-str-flat", 0)

	// CALL-INIT, ARRAY-field payload struct: same admission, array field freed
	// via __struct_drop_<S>'s arr_dec.
	run(t, `struct S { xs: i32[], n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(n: i32): E { return A(S { xs: [n, n + 1], n: n }, n); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = mk(i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var e2: E = mk(j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "rcenum-call-init-arr-flat", 0)

	// SCALAR-ONLY struct payload (#4355 slice 8): S { m, n } has no reclaimable
	// leaf, so nested_field_deep_drop_ok rejected it and the chain leaked; the
	// widened enum_field_rc_droppable admits it and the consume releases the
	// payload box with a single rc_dec. Churn flat at detector zero.
	run(t, `struct S { m: i32, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(n: i32): E { return A(S { m: n, n: n + 1 }, n); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var e: E = mk(i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var e2: E = mk(j);
        match (e2) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, "rcenum-scalar-struct-payload-flat", 0)

	// SCALAR-ONLY struct payload, ALIASED (bare-ident) — the fresh-literal gate
	// (variant_struct_payloads_fresh) must reject it: s0's box is swept by its
	// own local reclaim, so a consume-site free would double-free. s0 stays
	// readable at detector zero (the chain keeps the sound leak).
	run(t, `struct S { m: i32, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var s0: S = S { m: 7, n: 8 };
        var e: E = A(s0, i);
        match (e) { A(s, k) => { if (s.m != 7) { bad = 1; } }, B(x, y) => { bad = 1; } }
        if (s0.n != 8) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "rcenum-scalar-struct-aliased-payload-safe", 0)

	// PARAM-EMBED exclusion: mk stores its param string into the payload field
	// (`S { name: nm }`) — the RCE scan must reject it (freeing would dangle
	// the caller's string). keep stays readable, detector zero, chain leaks
	// (sound).
	run(t, `struct S { name: string, n: i32 }
enum E { A(S, i32), B(i32, i32) }
function mk(nm: string, n: i32): E { return A(S { name: nm, n: n }, n); }
function main(): i32 {
    var keep: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var e: E = mk(keep, i);
        match (e) { A(s, k) => { acc = acc + k; }, B(x, y) => { acc = acc + x + y; } }
        i = i + 1;
    }
    if (keep.len() != 4) { return 88; }
    if (__rc_underflow() != 0) { return 99; }
    return 0;
}`, "rcenum-call-init-param-embed-safe", 0)

	// RETURN-CLOBBER regression pin: the DIRECT-init struct-payload consume
	// (the shipped TestSelfHostEnumStructPayloadDropIRX86_64 shape) at
	// DETECTOR ZERO — the shipped test asserts boundedness only, which the
	// clobbered return passed while ticking the detector once per consume.
	run(t, `struct Inner { items: i32[] }
enum Box { Full(Inner), Empty }
function readit(): i32 {
    var b: Box = Full(Inner { items: [1,2,3,4] });
    var r: i32 = 0;
    match (b) { Full(inner) => { r = inner.items[0]; }, Empty => { r = 0; } }
    return r;
}
function main(): i32 {
    var s: i32 = 0;
    var f: i32 = 0;
    while (f < 2000) { s = s + readit(); f = f + 1; }
    if (s != 2000) { return 97; }
    return __rc_underflow();
}`, "rcenum-struct-payload-detector-zero", 0)

	// STRING-field payload, DIRECT init: the shape the clobbered return
	// SEGFAULTED on (__fern_str_free on the twice-freed field) — now flat at
	// detector zero.
	run(t, `struct S { name: string, n: i32 }
enum E { A(S), Empty }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 2000) {
        var e: E = A(S { name: "a" + "b", n: i });
        match (e) { A(_) => { acc = acc + 1; }, Empty => { acc = acc + 0; } }
        i = i + 1;
    }
    if (acc != 2000) { return 97; }
    return __rc_underflow();
}`, "rcenum-str-field-direct-detector-zero", 0)
}
