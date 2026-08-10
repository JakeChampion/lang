package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// litStrLocalReclaimCases pin #6582: a literal-initialised string local declared INSIDE
// a loop body leaked one box per iteration on the asm backends.
//
// str_local_binding_is_fresh admitted a concat (`a + b`) and the string producer methods
// but not a bare literal, so `var pre: string = "ab"` earned no "STR:" credit and was
// never freed. is_fresh_str_temp — twenty lines below it — already documents why the
// literal IS fresh: const_str allocates a fresh box per evaluation, the DATA is .rodata
// but the box is not, and __fern_str_free's heap-base guard skips the data and reclaims
// the box. It admitted exactly this shape as a concat OPERAND; a named binding is no
// different.
//
// Measured with FERN_LEAKCHECK=1 (allocs/frees/live_bytes), 200 iterations, self-host
// x86-64 — `__heap_bump_bytes()` deltas cannot see this, see #5474's retraction:
//
//	while (…) { var pre = "ab"; acc += pre.len(); }   200/0/4800    -> 200/199/24
//	the same with an i32[] built from pre.len()       400/200/4800  -> 400/399/24
//	pre HOISTED above the loop (control)              201/200/24    -> 201/201/0
//
// Flat on wasm before and after, where the whole literal is data-section and arr_dec is
// a guarded no-op. That backend split is what made it look like a partial fix to whatever
// class was under test — it cost this repo a full round on #5474's gate, whose churn cases
// hoist the string for that reason.
//
// Two shapes stay UNCREDITED on purpose, both because the alternative is an
// over-release rather than a leak:
//
//   - A literal local that is also a CONCAT OPERAND (`var pre = "ab"; var q = pre + "x";`
//     — 800/598/4832, still leaking). The shared escape gate expr_unsafe_for treats any
//     ident operand of a binary op as an escape, so `pre` earns no credit. That gate
//     backs every reclaim class, not just strings, so widening it is a cross-class change
//     needing its own gating rather than a rider here.
//   - A literal local that is the receiver of `.to_string()`. On a string receiver that
//     call is the IDENTITY, so its result aliases the receiver's box and the concat-temp
//     machinery frees that result as an inline-consumed temp; crediting the local too
//     would release the same box twice. litstr_tostring_receiver excludes it, which is
//     what keeps TestSelfHostStrConcatTemp's `tostring-string-recv-alias-safe` pinned at
//     exactly two sites.
//
// The credit also stays out of str_local_binding_is_fresh, whose ~20 other callers drive
// the accumulator and concat-temp analyses: folding it in there made `s = "reset"` read
// as a fresh rebind and admitted an accumulator TestSelfHostStrAccum pins as
// un-reclaimable.
var litStrLocalReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The reproducer: a literal string local re-declared per iteration, used borrow-only.
	{"litstr-loop-local", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var pre: string = "ab"; acc = (acc + pre.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var p2: string = "cd"; acc = (acc + p2.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// The shape #5474's gate tripped on: the same local feeding a scalar array build.
	{"litstr-loop-local-with-array", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var pre: string = "ab"; var xs: i32[] = [pre.len(), 2]; acc = (acc + xs.len()) % 251; i = i + 1; }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 5000) { var p2: string = "cd"; var ys: i32[] = [p2.len(), 2]; acc = (acc + ys.len()) % 251; j = j + 1; }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 512) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0},
	// VALUE guard: the freed box must not be read back. A literal local re-declared per
	// iteration and compared, so a premature free or a shared/interned box shows up as a
	// wrong answer rather than only as a byte count. 200 rounds x 2 = 400, %251 = 149.
	{"litstr-value-exact", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var pre: string = "ab";
        if (pre != "ab") { return 97; }
        acc = (acc + pre.len()) % 251;
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 149) { return 97; }
    return 0;
}`, 0},
	// ESCAPE negative: the literal local is returned, so it must NOT be freed.
	{"litstr-escape-return-safe", `function mk(): string { var pre: string = "abcd"; return pre; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { var s: string = mk(); acc = (acc + s.len()) % 251; i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    if (acc != 47) { return 97; }
    return 0;
}`, 0},
}

// TestSelfHostLitStrLocalReclaimIRX86_64 drives the cases through the self-hosted x86-64
// compiler (asm_run), heap-bump + underflow guarded.
func TestSelfHostLitStrLocalReclaimIRX86_64(t *testing.T) {
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

	for _, tc := range litStrLocalReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = literal string local leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostLitStrLocalReclaimIRArm64 is the arm64 leg — the other backend that
// allocates a box for a string literal, and so the other one that leaked.
func TestSelfHostLitStrLocalReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range litStrLocalReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = literal string local leaked; 99 = over-release/underflow; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}
