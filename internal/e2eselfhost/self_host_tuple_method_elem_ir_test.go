package e2eselfhost

import (
	"os/exec"
	"testing"
)

// tupleMethodElemCases pin a tuple literal whose element is a value-position
// if/match with a METHOD-call arm.
//
// tuple_elem_ctor_eligible classifies an IIFE element by its leaf branch value
// and admitted a CALL element in two forms: a free function with a
// statically-known scalar result, and a tuple-element closure call (`t.0(3)`).
// A method call — an ExprFieldAccess callee with a non-numeric field — matched
// neither, so `(p.sum(), 165i64)` bailed the whole module to the AST path while
// `(f(6), 165i64)` lowered. The difference was nothing but how the callee is
// spelled (#6584).
//
// iife_method_ret_i32 is the sound-by-exclusion answer the free-fn side already
// had: a method registered as returning a string / array / tuple / struct /
// Option is rejected, and its wide sibling names an i64 / f64 result. Both are
// the same lookups the value-position match arms use.
//
// free-fn-elem-unchanged and literal-arms-unchanged are the guards: the two
// forms that already lowered must still take exactly the path they did.
var tupleMethodElemCases = []struct {
	name string
	src  string
	exit int
}{
	// Reduced from fernsmith seed 430, then rewritten to CONSUME the tuple —
	// the reduced seed never calls gen_f1, so it shows the bail and nothing else.
	{"method-arm-in-tuple-elem", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i64) { return ((if (c) { p1.sum() } else { 6i32 }), 165i64); } function main(): i32 { var t: (i32, i64) = gen(Pair { fst: 1i32, snd: 7i32 }, true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
	// Both arms methods, and the else branch taken.
	{"method-arms-both", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i64) { return ((if (c) { p1.sum() } else { p1.fst }), 165i64); } function main(): i32 { var t: (i32, i64) = gen(Pair { fst: 1i32, snd: 7i32 }, false); return (t.0 + (t.1 as i32)) & 63i32; }`, 38},
	// No 64-bit element: the gap is the element's ADMISSION, not its width.
	{"method-arm-all-i32-tuple", `struct Pair { fst: i32, snd: i32 } function (p: Pair) sum(): i32 { return p.snd; } function gen(p1: Pair, c: boolean): (i32, i32) { return ((if (c) { p1.sum() } else { 6i32 }), 165i32); } function main(): i32 { var t: (i32, i32) = gen(Pair { fst: 1i32, snd: 7i32 }, true); return (t.0 + t.1) & 63i32; }`, 44},
	{"free-fn-elem-unchanged", `function f(n: i32): i32 { return n + 1i32; } function gen(c: boolean): (i32, i64) { return ((if (c) { f(6i32) } else { 6i32 }), 165i64); } function main(): i32 { var t: (i32, i64) = gen(true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
	{"literal-arms-unchanged", `function gen(c: boolean): (i32, i64) { return ((if (c) { 7i32 } else { 6i32 }), 165i64); } function main(): i32 { var t: (i32, i64) = gen(true); return (t.0 + (t.1 as i32)) & 63i32; }`, 44},
}

// TestSelfHostTupleMethodElemIRX86_64 — the x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostTupleMethodElemIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleMethodElemCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostTupleMethodElemIRArm64 — the arm64 IR path, where the self-host
// compiler produces the finished binary itself.
func TestSelfHostTupleMethodElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleMethodElemCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
