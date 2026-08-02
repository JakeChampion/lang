package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostClosureCaptureBorrowIRX86_64 pins the #4354 borrow fix for
// hoisted-closure capture extracts. make_clo_func synthesizes each capture
// read as `var <cap> = __env[1+i]` at the top of a `$clo`/`$wrap` body; the
// lambda's exit dec-sweep treated those as OWNED array locals and
// shallow-dec'd them on EVERY call — but the env box owns the references,
// so an rc==1 capture was freed out from under the box's owner on the
// first call (a per-call use-after-free; 2 underflow ticks on the 2-call
// escaping shape below, on unmodified pre-fix main). lower_func now
// registers env-extract names as "ENVCAP:" borrows and the sweep skips
// them — a borrow is not released by the borrower.
func TestSelfHostClosureCaptureBorrowIRX86_64(t *testing.T) {
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
			t.Errorf("%s exited %d, want %d (nonzero underflow = a borrowed capture extract was released)", name, code, want)
		}
	}

	// ESCAPING closure called twice through a fn-typed param: pre-fix each
	// call's exit sweep dec'd the extracted xs — the first freed it live
	// (rc==1), the second ticked the detector. Post-fix: underflow 0 and
	// the value still correct.
	run(t, `function call2(f: () => i32): i32 { return f() + f(); }
function go(k: i32): i32 {
    var xs: i32[] = [k, k + 1];
    var c = () => xs[0] + xs[1];
    return call2(c) / 2;
}
function main(): i32 {
    var q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow();
}`, "escaping-capture-borrow-detector-zero", 0)

	// ALIAS shape (the #4557 fix routes it onto the hoisted path): both
	// names call the same env; the extract borrow must survive both calls.
	run(t, `function go(k: i32): i32 {
    var xs: i32[] = [k, k + 1];
    var c = () => xs[0] + xs[1];
    var d = c;
    var a: i32 = c();
    var b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 {
    var q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow();
}`, "alias-capture-borrow-detector-zero", 0)

	// CHURN: 6000 iterations across two heap-bump measurements — detector
	// zero AND flat (the borrow exclusion must not reintroduce the env or
	// capture leak the #4552 slice closed for approved closures).
	run(t, `function go(k: i32): i32 {
    var xs: i32[] = [k, k + 1];
    var c = () => xs[0] + xs[1];
    var d = c;
    var a: i32 = c();
    var b: i32 = d();
    if (a != b) { return 98; }
    return a;
}
function main(): i32 {
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 3000) { acc = (acc + go(i)) % 251; i = i + 1; }
    var b1: i32 = __heap_bump_bytes();
    i = 0;
    while (i < 3000) { acc = (acc + go(i)) % 251; i = i + 1; }
    var b2: i32 = __heap_bump_bytes();
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 97; }
    return 0;
}`, "alias-capture-churn-flat-detector-zero", 0)
}

// TestSelfHostClosureCaptureBorrowIRArm64: the escaping shape under qemu.
func TestSelfHostClosureCaptureBorrowIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	prog := `function call2(f: () => i32): i32 { return f() + f(); }
function go(k: i32): i32 {
    var xs: i32[] = [k, k + 1];
    var c = () => xs[0] + xs[1];
    return call2(c) / 2;
}
function main(): i32 {
    var q: i32 = go(3);
    if (q != 7) { return 90 + q; }
    return __rc_underflow();
}`
	asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(prog), "-ir", "-target", "arm64")
	if len(asm) == 0 {
		t.Fatalf("self-host arm64 compiler emitted 0 bytes")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "escaping-capture-borrow-arm64", string(asm))
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("escaping-capture-borrow-arm64 exited %d, want 0 (nonzero underflow = borrowed extract released)", code)
	}
}
