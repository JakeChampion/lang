package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnClosureContainerIRCases pin RETURNING a closure/fn value held in a
// container across a function boundary — the struct-field / param variants of
// issue #5202 (cases B, C, E). closure_ret_fns_of / closurearr_ret_fns_of (the
// pre-passes registering functions whose body returns a closure box, so a
// caller's `var g = pick()` binds g a closure local / closure array) gained a
// `structs`-aware type resolver (detector_expr_type) and now recognise:
//   B — `return r.hs[0]` / `return fs[i]`: an element of a closure ARRAY that is
//       a struct field or an fn[] param (type resolves to "fn[]").
//   C — `return r.hs`: a whole closure-array struct field / fn[] param.
//   E — `return kvs[i].f` / `return s.f`: a fn-valued struct FIELD.
// Before the fix these went unregistered, the caller bound g a plain scalar,
// and `g()` bare-called the box pointer → SIGSEGV. Case A (`return hs[0]` from a
// LOCAL closure array) landed earlier in #5207; case D (a MATCH-bound fn-typed
// enum payload) is a separate mechanism still open in #5202.
//
// Found via differential probing. Exit codes cross-checked against the
// interpreter and the native Go backend.
var returnClosureContainerIRCases = []struct {
	name string
	src  string
	exit int
}{
	// B — struct-field closure-array element.
	{"field-elem", "struct Reg { hs: (() => i32)[] } function pick(r: Reg): () => i32 { return r.hs[0]; } function main(): i32 { var n: i32 = 8; var r = Reg { hs: [() => n] }; var g = pick(r); return g(); }", 8},
	// C — whole closure-array struct field, indexed at the caller.
	{"whole-arr", "struct Reg { hs: (() => i32)[] } function pick(r: Reg): (() => i32)[] { return r.hs; } function main(): i32 { var n: i32 = 8; var hs = pick(Reg { hs: [() => n] }); return hs[0](); }", 8},
	// E — fn-valued struct field selected from a struct-array element (registry).
	{"registry", "struct KV { k: i32, f: () => i32 } function lookup(kvs: KV[], key: i32): () => i32 { var i: i32 = 0; while (i < kvs.len()) { if (kvs[i].k == key) { return kvs[i].f; } i = i + 1; } return () => 0; } function main(): i32 { var n: i32 = 8; var kvs: KV[] = [KV { k: 1, f: () => n }, KV { k: 2, f: () => n * 2 }]; var g = lookup(kvs, 2); return g(); }", 16},
	// B — element of an fn[] PARAM (not a field): `return fs[i]`.
	{"param-fnarr", "function get(fs: (() => i32)[], i: i32): () => i32 { return fs[i]; } function main(): i32 { var n: i32 = 5; var g = get([() => n, () => n + 2], 1); return g(); }", 7},
	// E — fn-valued scalar struct field: `return s.f`.
	{"scalar-field", "struct S { f: () => i32 } function pick(s: S): () => i32 { return s.f; } function main(): i32 { var n: i32 = 9; var g = pick(S { f: () => n }); return g(); }", 9},
	// Argument-taking closure returned from a struct-field element.
	{"field-elem-arg", "struct Reg { hs: ((i32) => i32)[] } function pick(r: Reg): (i32) => i32 { return r.hs[0]; } function main(): i32 { var n: i32 = 5; var r = Reg { hs: [(x: i32) => x + n] }; var g = pick(r); return g(10); }", 15},
}

// TestSelfHostReturnClosureContainerIRX86_64 — the x86-64 irlower fix, through
// the production driver (asm_ir_run `-ir`).
func TestSelfHostReturnClosureContainerIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureContainerIRCases {
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

// TestSelfHostReturnClosureContainerIRArm64 — CI-gated arm64 counterpart. The
// fix is in the shared irlower.fern, so the arm64 IR backend picks it up.
func TestSelfHostReturnClosureContainerIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-closure-container gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnClosureContainerIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
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
