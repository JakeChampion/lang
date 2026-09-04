package e2eselfhost

import (
	"os/exec"
	"testing"
)

// #8240: a self-host callee that hands back a BORROWED STRUCT parameter's box
// gave its caller a second UNCOUNTED name for it, and the caller then freed the
// box while a live binding still named it.
//
// The caller's half is release_last_use_source: after `var q2 = g(.., name, ..)`
// where `name` dies at that call, it decs on the `old == q2` arm — the arm whose
// own comment states the contract "q2 holds its own count on it". The callee did
// not give it one, so the box went rc 1 -> 0, was freed, and the next
// __fern_arr_box handed the same block to the following literal.
//
// The two halves were never matched. last_use_release_slot bails on
// is_arr_slot, so the caller-side release is STRUCT-only; the callee-side
// return-inc (ret-borrowed-param) tested is_arr_slot, so it was ARRAY-only.
// Arrays are safe because no release fires for them, not because the callee was
// right — array-param-control below pins that, and it passed before the fix too.
//
// NOTHING ELSE CATCHES THIS. The free is at rc 1, so the underflow counter
// never trips, and the recycled block normally comes back field-identical
// because the next allocation is the same struct shape — which is why it
// survived every rc gate. Reading a DIFFERENTLY-shaped literal out of the
// recycled block is what exposes it, and that is what the `junk` bindings are
// for: remove them and every case here passes against a broken compiler.
//
// STILL OPEN, deliberately not asserted here. Only the callee returning the
// PARAMETER is fixed. Returning a LOCAL bound from it (`var st = s; return st`)
// is the same defect and still reproduces on this commit — `ret_alias(0, s1)`
// exits 42. Closing it needs the alias bind to be counted, and a retain there
// has to be paired with a release: the bind sets alias_inc, but the alias slot
// is not exit-swept, because slot_is_reclaimable_struct refuses a slot bound
// from a parameter. Adding the retain alone moved
// struct_arr_field__{fnscope,if_block}__alias_param from clean to LEAK in the
// leak matrix, which is a regression the matrix names as such; granting the
// slot the box-only release role that would balance it is credit surgery whose
// failure mode is a double free. That half stays on #8240 with this analysis
// rather than being half-landed or papered over with a rebanked row.
//
// Differential against `fern -interp`, not a written-down number: native and
// the interpreter both answer correctly, so the oracle is real. Assertions are
// on the ANSWER and __rc_underflow_count() — deliberately never on live_bytes,
// because this shape still leaks 56 B on the self-host legs (pre-existing, and
// unrelated to the free), so a byte assertion would fail for the wrong reason.
var selfHostBorrowedStructParamCases = []struct {
	name string
	src  string
}{
	// The fixed shape: the callee returns the borrowed parameter itself, so the
	// caller holds two names for one box and releases one of them.
	{"return-param", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_param(s: St): St { return s; }\nfunction main(): i32 {\n    var s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    var s1: St = s.emit(1);\n    var a: St = ret_param(s1);\n    var junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	// Control on the other side of the retain: the local IS rebound before the
	// return, so a fresh box comes back and the caller's box was never handed
	// out. A retain that fired here would strand the fresh box instead.
	{"return-alias-rebound", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_alias(n: i32, s: St): St {\n    var st: St = s;\n    var i: i32 = 0;\n    while (i < n) { st = st.emit(i); i = i + 1; }\n    return st;\n}\nfunction main(): i32 {\n    var s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    var s1: St = s.emit(1);\n    var a: St = ret_alias(2, s1);\n    var junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	// Control: an ARRAY param handed back the same way. Correct before the fix
	// as well as after — no caller-side release fires for arrays — so this pins
	// that widening the return-inc to structs did not disturb the array path.
	{"array-param-control", "@noinline\nfunction ret_alias(n: i32, a: i32[]): i32[] {\n    var t: i32[] = a;\n    var i: i32 = 0;\n    while (i < n) { t = t.append(i); i = i + 1; }\n    return t;\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    var a1: i32[] = a.append(1);\n    var a2: i32[] = ret_alias(0, a1);\n    var junk: i32[] = [7, 7, 7, 7, 7];\n    if (junk[0] != 7) { return 81; }\n    return a2.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostBorrowedStructParamReturnX86_64 — the production x86-64 IR path
// against the interpreter oracle. return-param exits 42 without the fix.
func TestSelfHostBorrowedStructParamReturnX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostBorrowedStructParamCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src), "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "bsp_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostBorrowedStructParamReturnArm64 — the same cases through the arm64
// emit. The retain is shared irlower analysis rather than per-backend emission,
// so this leg is what would catch it landing on only one register backend.
func TestSelfHostBorrowedStructParamReturnArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostBorrowedStructParamCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, "bsp_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
