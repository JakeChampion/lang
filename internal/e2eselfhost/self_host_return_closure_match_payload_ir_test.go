package e2eselfhost

import (
	"os/exec"
	"testing"
)

// returnMatchPayloadIRCases pin RETURNING a fn value bound by a MATCH ARM to an
// enum payload across a function boundary — case D of issue #5202, the last of
// the container-held-fn-value cluster (A: local closure-array element #5207;
// B/C/E: struct-field / param closure-array element or fn field #5252). The
// closure_ret_fns_of return scan's ExprIdent arm only recognised a `var`-bound
// closure local (body_binds_closure_local); a `match (b) { W(f) => return f }`
// binding was missed, so the caller bound `g` a plain scalar and `g()` bare-
// called the closure box pointer → SIGSEGV. body_binds_fn_payload_via_match now
// recognises a match-arm binding to a fn-typed enum payload (the variant's `__ev`
// field resolves to "fn" via the structs-only variant_payload_field_type), so the
// factory is registered closure-returning and the caller dispatches env-first.
//
// A narrower sibling defect (returning a match-bound payload from one branch and
// a LAMBDA from another in the same fn-typed function) is tracked separately in
// #5266 — a return-value-representation change, out of scope here.
//
// Found via differential probing. Exit codes cross-checked against the
// interpreter and the native Go backend.
var returnMatchPayloadIRCases = []struct {
	name string
	src  string
	exit int
}{
	// D — the canonical repro: `match (b) { W(f) => { return f; } }`.
	{"plain", "enum Box { W(() => i32) } function pick(b: Box): () => i32 { match (b) { W(f) => { return f; } } } function main(): i32 { var n: i32 = 9; var g = pick(Box.W(() => n)); return g(); }", 9},
	// Argument-taking payload closure.
	{"arg-payload", "enum Box { W((i32) => i32) } function pick(b: Box): (i32) => i32 { match (b) { W(f) => { return f; } } } function main(): i32 { var n: i32 = 5; var g = pick(Box.W((x: i32) => x + n)); return g(10); }", 15},
	// Multiple fn-payload variants, each arm returns its own binding.
	{"multi-arm", "enum Box { A(() => i32), B(() => i32) } function pick(b: Box): () => i32 { match (b) { A(f) => { return f; }, B(g) => { return g; } } } function main(): i32 { var n: i32 = 3; var h = pick(Box.B(() => n * 7)); return h(); }", 21},
	// Qualified variant pattern (`Box.W(f)`), same as plain otherwise.
	{"qualified-pat", "enum Box { W(() => i32) } function pick(b: Box): () => i32 { match (b) { Box.W(f) => { return f; } } } function main(): i32 { var n: i32 = 4; var g = pick(Box.W(() => n)); return g(); }", 4},
	// Return of the payload from inside a nested `if` (both branches return it).
	{"nested-if", "enum Box { W(() => i32) } function pick(b: Box, flag: i32): () => i32 { match (b) { W(f) => { if (flag > 0) { return f; } else { return f; } } } } function main(): i32 { var n: i32 = 6; var g = pick(Box.W(() => n), 1); return g(); }", 6},
	// Control: `return f()` (CALL the payload, not return it) — not a closure
	// return, must stay unregistered and still evaluate correctly.
	{"call-not-return", "enum Box { W(() => i32) } function pick(b: Box): i32 { match (b) { W(f) => { return f(); } } } function main(): i32 { var n: i32 = 11; return pick(Box.W(() => n)); }", 11},
	// Control: a NON-fn (i32) payload return must NOT be misclassified as a
	// closure (variant_payload_field_type resolves "i32", not "fn").
	{"nonfn-payload", "enum Box { W(i32) } function pick(b: Box): i32 { match (b) { W(v) => { return v; } } } function main(): i32 { var g = pick(Box.W(42)); return g; }", 42},
}

// TestSelfHostReturnMatchPayloadIRX86_64 — the x86-64 irlower fix, through the
// production driver (asm_ir_run `-ir`).
func TestSelfHostReturnMatchPayloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnMatchPayloadIRCases {
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

// TestSelfHostReturnMatchPayloadIRArm64 — CI-gated arm64 counterpart. The fix is
// in the shared irlower.fern, so the arm64 IR backend picks it up.
func TestSelfHostReturnMatchPayloadIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 return-match-payload gate needs a native x86 host to run the driver")
	}
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range returnMatchPayloadIRCases {
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
