package e2ecompiler

import (
	"os/exec"
	"testing"
)

// cloArrayFieldBindCases pin the BIND-then-call and for-loop shapes of a
// closure-array element loaded from a struct field — issue #5160 defect #2
// (the segfault sibling of the direct-call defect #1 that
// TestSelfHostCloArrayFieldCallIR* covers). These lower on the IR path (NOT the
// bailing): the struct-field closure array `r.hs` is `fn[]`, whose element
// is a closure BOX, but before the fix irlower bound the element / the whole
// array as a plain scalar/array local, so the subsequent `f()` / `fns[i]()` /
// `for h in r.hs { h() }` emitted a bare `call *reg` on the box POINTER — jumping
// into the box's data and SIGSEGVing. The fix marks a local bound from a
// closure-array field (element or whole-array alias) is_closurearr /
// closure-local, so the call dispatches env-first (box[0] = fn_addr, box passed
// as __env). The for-loop uses the same fix via lower_foreach_snapshot's hidden
// `let $forit = r.hs`.
//
// RC-soundness follow-up: the bound element / foreach loop var is a BORROW of the
// struct-owned closure box (the struct's field reclaim frees it), so it must NOT
// be marked is_arr — otherwise the function-exit sweep decs the box a second time
// and underflows its rc. The `rc-soundness` case is a churn-loop probe
// (__rc_underflow_count / __heap_bump_bytes) pinning this: element bind, direct
// foreach, and whole-field-alias foreach each reclaim their boxes exactly once.
//
// Exit codes cross-checked against the interpreter and the native Go backend.
var cloArrayFieldBindCases = []struct {
	name string
	src  string
	exit int
}{
	// let f = r.hs[0]; f() — the canonical repro B.
	{"bind", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 40; let r = Reg { hs: [() => n] }; let f = r.hs[0]; return f(); }", 40},
	// let fns = r.hs; fns[0]() — whole-array alias, then indexed call.
	{"alias", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 40; let r = Reg { hs: [() => n] }; let fns = r.hs; return fns[0](); }", 40},
	// for h in r.hs { acc += h() } — the for-loop idiom, two capturing elements.
	{"for", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 2; let r = Reg { hs: [() => n, () => n + 1] }; let acc: i32 = 0; for h in r.hs { acc = acc + h(); } return acc; }", 5},
	// Bind of an element from a closure taking an argument.
	{"bind-arg", "struct Reg { hs: ((i32) => i32)[] } function main(): i32 { let n: i32 = 5; let r = Reg { hs: [(x: i32) => x + n] }; let f = r.hs[0]; return f(10); }", 15},
	// alias then for-loop over the aliased local (both fixes together).
	{"alias-for", "struct Reg { hs: (() => i32)[] } function main(): i32 { let n: i32 = 3; let r = Reg { hs: [() => n, () => n * 2] }; let fns = r.hs; let acc: i32 = 0; for h in fns { acc = acc + h(); } return acc; }", 9},
	// RC soundness: run the element-bind / direct-foreach / whole-field-alias
	// shapes N times and probe for over-release (__rc_underflow_count) and unbounded
	// heap growth (__heap_bump_bytes) — the borrowed element boxes must be
	// reclaimed by the owning struct exactly once, never dec'd a second time by
	// the binding's / loop var's exit-sweep.
	{"rc-soundness", "struct Reg { hs: (() => i32)[] } function one(k: i32): i32 { let r = Reg { hs: [() => k, () => k + 1] }; let f = r.hs[0]; let acc: i32 = f(); for h in r.hs { acc = acc + h(); } let fns = r.hs; for g in fns { acc = acc + g(); } return acc; } function churn(n: i32): i32 { let i: i32 = 0; let s: i32 = 0; while (i < n) { s = one(i); i = i + 1; } return s; } function main(): i32 { let w: i32 = churn(3000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(3000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 4096) { return 98; } if (w != x) { return 97; } return 0; }", 0},
}

// TestSelfHostCloArrayFieldBindIRX86_64 — the x86-64 fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostCloArrayFieldBindIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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

// TestSelfHostCloArrayFieldBindIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared lowering, so the arm64 IR backend picks it up for free;
// this pins that. Mirrors TestSelfHostCloArrayFieldCallIRArm64.
func TestSelfHostCloArrayFieldBindIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 clo-array-field-bind gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range cloArrayFieldBindCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
