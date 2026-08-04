package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLiteralArgReclaimIRX86_64 pins #4355 slice 6: a string-LITERAL
// call arg allocates a fresh 16-byte rc-headered box per evaluation
// (const_str; its .rodata data is heap-guard-skipped), and nothing freed it —
// one box leaked per call in any loop passing literal string args. At a
// BORROWABLE param position of a known free function (borrowable_params_of:
// provably borrow-read-only, never escaping — so the callee cannot retain or
// return the arg) the call lowering now stashes the literal arg and frees it
// right after the call via the rc-aware __fern_str_free, net-zero on the
// operand stack under the live result. Non-borrowable positions (returned /
// stored / forwarded args) keep the sound leak — pinned below.
func TestSelfHostLiteralArgReclaimIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (98 = literal-arg box leaked; 99 = over-release; 88 = live value freed; 97 = value corrupted)", name, code, want)
		}
	}

	// Literal arg at a borrowable position — churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit("ab"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit("ab"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 4400) { return 97; }
    return 0;
}`, "literal-arg-borrowable-flat", 0)

	// NON-borrowable position (callee RETURNS its param): the literal box is
	// retained by the binding, so it must NOT be freed at the call edge — the
	// bound value stays readable at detector zero (the box keeps its prior
	// sound leak).
	run(t, `function keepit(nm: string): string { return nm; }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        var got: string = keepit("xy");
        if (got.len() != 2) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 88; }
    return 0;
}`, "literal-arg-retained-safe", 0)

	// FRESH CONCAT arg (#4355 slice 7): `readit(base + "bc")` — the concat
	// byte-copies into a fresh anonymous temp; is_fresh_str_temp admits it at
	// the borrowable position and the post-call free reclaims it. base (an
	// operand, only read) stays readable; churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var base: string = "a";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit(base + "bc"); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit(base + "bc"); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (base.len() != 1) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 6600) { return 97; }
    return 0;
}`, "fresh-concat-arg-flat", 0)

	// FRESH PRODUCER-METHOD arg (#4355 slice 7): `readit(src.to_ascii_upper())` —
	// a copying string method's result is a fresh temp; reclaimed after the
	// call, the receiver survives, churn flat at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var src: string = "AbC" + "d";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + readit(src.to_ascii_upper()); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + readit(src.to_ascii_upper()); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 8800) { return 97; }
    return 0;
}`, "fresh-producer-arg-flat", 0)

	// BARE-IDENT arg trap: `readit(src)` aliases a live local —
	// is_fresh_str_temp excludes it, so NO free is emitted; src stays
	// readable across every call at detector zero.
	run(t, `function readit(nm: string): i32 { return nm.len(); }
function main(): i32 {
    var src: string = "aa" + "bb";
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 500) {
        if (readit(src) != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (src.len() != 4) { return 88; }
    if (bad != 0) { return 87; }
    return 0;
}`, "bare-ident-arg-safe", 0)

	// MIXED args: the literal frees, the LIVE ident arg is untouched (a
	// borrowed read — no stash, no free) and stays readable; churn flat.
	run(t, `function pick(a: string, b: string): i32 { return a.len() + b.len(); }
function main(): i32 {
    var live: string = "aa" + "bb";
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + pick("x", live); i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + pick("x", live); j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (live.len() != 4) { return 88; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc != 11000) { return 97; }
    return 0;
}`, "literal-arg-mixed-flat", 0)
}
