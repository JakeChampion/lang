package e2eselfhost

import (
	"os/exec"
	"testing"
)

// #8240: a self-host caller freed a struct box while a live binding still
// named it.
//
// `var q2: T = g(.., name, ..)` where `name` dies at that call routes through
// release_last_use_source, whose `old == q2` arm dec'd name's box on the
// grounds that "q2 holds its own count on it". It does not: a callee that
// hands back a borrowed struct param returns an UNCOUNTED alias, so that count
// was the only one. rc 1 -> 0, freed, and the next __fern_arr_box handed the
// same block to the following literal.
//
// The fix drops that dec — the slot's count transfers to q2, whose own sweep
// frees the box — rather than adding the matching retain in the callee. The
// retain is what native does and it is the wrong half to move here: a
// materialised struct call result is released through an __fern_rc_is_unique
// gate that a second count turns off, so retaining strands the box instead.
// The conformance fixture alloc_flat_method_identity_return measures exactly
// that and reports "grows".
//
// NOTHING ELSE CAUGHT THIS. The free is at rc 1, so the underflow counter
// never trips, and the recycled block normally comes back field-identical
// because the next allocation is the same struct shape — which is why it
// survived every rc gate. Reading a DIFFERENTLY-shaped literal out of the
// recycled block is what exposes it, and that is what the `junk` bindings are
// for: remove them and every case here passes against a broken compiler.
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
	// The callee returns the borrowed parameter itself.
	{"return-param", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_param(s: St): St { return s; }\nfunction main(): i32 {\n    var s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    var s1: St = s.emit(1);\n    var a: St = ret_param(s1);\n    var junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	// The same box handed back through a LOCAL bound from the parameter — the
	// issue's own shape, and the half a callee-side retain could not close.
	// The caller cannot tell the two apart, which is why the fix belongs on
	// this side: the runtime `old == q2` compare answers both.
	{"return-alias-passthrough", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_alias(n: i32, s: St): St {\n    var st: St = s;\n    var i: i32 = 0;\n    while (i < n) { st = st.emit(i); i = i + 1; }\n    return st;\n}\nfunction main(): i32 {\n    var s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    var s1: St = s.emit(1);\n    var a: St = ret_alias(0, s1);\n    var junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	// The other side of the compare: the local IS rebound before the return, so
	// a FRESH box comes back and the caller's box is genuinely dead. What this
	// row can show is only that the non-equal path still answers correctly —
	// the release it must keep is a LEAK question, and a leak moves neither the
	// exit code nor __rc_underflow_count(). That direction is carried by the
	// leak matrix and the conformance leak census, not here.
	{"return-alias-rebound", "struct St { ops: i32[], names: string[], ctrl: i32 }\n@noinline\nfunction (s: St) emit(op: i32): St {\n    return St { ...s, ops: s.ops.append(op), ctrl: s.ctrl + 1 };\n}\n@noinline\nfunction ret_alias(n: i32, s: St): St {\n    var st: St = s;\n    var i: i32 = 0;\n    while (i < n) { st = st.emit(i); i = i + 1; }\n    return st;\n}\nfunction main(): i32 {\n    var s: St = St { ops: [], names: [\"alpha\"], ctrl: 0 };\n    var s1: St = s.emit(1);\n    var a: St = ret_alias(2, s1);\n    var junk: St = St { ops: [7], names: [\"zzz\"], ctrl: 42 };\n    if (junk.ctrl != 42) { return 81; }\n    return a.ctrl + __rc_underflow_count();\n}"},

	// Control: an ARRAY param handed back the same way. Arrays keep the
	// callee-side transfer retain (ret-borrowed-param) and no caller-side
	// release fires for them, so this is the untouched path.
	{"array-param-control", "@noinline\nfunction ret_alias(n: i32, a: i32[]): i32[] {\n    var t: i32[] = a;\n    var i: i32 = 0;\n    while (i < n) { t = t.append(i); i = i + 1; }\n    return t;\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    var a1: i32[] = a.append(1);\n    var a2: i32[] = ret_alias(0, a1);\n    var junk: i32[] = [7, 7, 7, 7, 7];\n    if (junk[0] != 7) { return 81; }\n    return a2.len() + __rc_underflow_count();\n}"},
}

// TestSelfHostBorrowedStructParamReturnX86_64 — the production x86-64 IR path
// against the interpreter oracle. return-param and return-alias-passthrough
// both exit 42 without the fix.
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
// emit. The release is shared irlower analysis rather than per-backend
// emission, so this leg is what would catch it landing on one register backend.
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
