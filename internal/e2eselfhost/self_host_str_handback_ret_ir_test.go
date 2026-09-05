package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStrHandbackRetIRX86_64 pins #8409: a fresh string argument at a
// BORROWABLE position whose callee RETURNS it through an identity method.
//
// The escape walker reads a bare-ident method receiver as an unconditional
// borrow, so `ret(x) { return x.idret(); }` leaves `x` borrowable even though
// `idret`'s no-op fast path (`return s`) hands `x`'s own box back. The caller
// then stashed the fresh argument temp and freed it unconditionally after the
// call — freeing the very box the call had just returned as its result: a
// double free (`__rc_underflow`) / use-after-free (`[[[[[]` for `[TOOL]`).
//
// The fix keeps the position borrowable but guards the post-call release on the
// result differing from the temp (str_handback_ret / free_stashed_str_args_
// guarded): on the identity path the temp IS the result, so the free is skipped
// and the result's new owner frees it once; on the allocating path the temp
// differs and is freed, so the leak the non-borrowable alternative would have
// opened (measured on the AL-01 conformance fixture) never appears. Both paths
// churn flat at detector zero. `maybe` returns its receiver bare in one arm, so
// it is a receiver-handback method; `remove_all(needle) { return
// s.replace(needle, ""); }` is the transitive std/string shape this mirrors.
func TestSelfHostStrHandbackRetIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (99 = double-free/over-release; 98 = leak; 97 = result corrupted; 88 = arg source freed)", name, code, want)
		}
	}

	// A method whose k==0 arm returns its receiver bare (identity) and whose
	// other arm allocates — the sometimes-identity/sometimes-fresh shape of
	// std/string's `replace`.
	method := `function (s: string) maybe(k: i32): string { if (k == 0) { return s; } return s + "!"; }
`

	// IDENTITY path: `idret` returns its arg via the identity arm. The fresh
	// concat temp aliases the call's result; the guard must skip freeing it, so
	// `got` stays readable and the churn is flat with no over-release.
	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function main(): i32 {
    var base: string = "payload";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var got: string = idret(base + "-x");
        if (got.len() != 9) { bad = 1; }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: string = idret(base + "-x");
        if (g2.len() != 9) { bad = 1; }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "identity-return-arg-flat", 0)

	// IDENTITY path, DISCARDED result: nothing binds the call result, so the
	// box the callee handed back is reclaimed by the discarded-result path. The
	// guard must still skip the arg free, or the two paths double-free it.
	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function main(): i32 {
    var base: string = "payload";
    var i: i32 = 0;
    while (i < 200) { idret(base + "-x"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { idret(base + "-x"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "identity-return-discarded-flat", 0)

	// FRESH path: `freshret` returns its arg via the allocating arm, so the
	// call result is a NEW box and the arg temp differs — the guard frees the
	// temp, closing the leak the non-borrowable escape-analysis fix opened.
	run(t, method+`function freshret(x: string): string { return x.maybe(1); }
function main(): i32 {
    var base: string = "payload";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var got: string = freshret(base + "-x");
        if (got.len() != 10) { bad = 1; }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: string = freshret(base + "-x");
        if (g2.len() != 10) { bad = 1; }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "fresh-return-arg-flat", 0)

	// TRANSITIVE: `wrap` hands its arg to `idret`, which hands it back — the
	// str_handback_ret fixpoint must chain the fact through `wrap`.
	run(t, method+`function idret(x: string): string { return x.maybe(0); }
function wrap(y: string): string { return idret(y); }
function main(): i32 {
    var base: string = "payload";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var got: string = wrap(base + "-x");
        if (got.len() != 9) { bad = 1; }
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var g2: string = wrap(base + "-x");
        if (g2.len() != 9) { bad = 1; }
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 97; }
    if (base.len() != 7) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    return 0;
}`, "transitive-handback-arg-flat", 0)
}
