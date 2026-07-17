package e2eselfhost

import (
	"os/exec"
	"testing"
)

// The self-host port of the #4873 containment (the native fix's sibling —
// see internal/e2e/append_borrowed_param_test.go): a callee's consume-form
// append (op_arr_push) mutates its receiver buffer in place at rc==1, and a
// borrowed PARAM's buffer is aliased by the caller at the same rc — so
// `var c = grow(a, 3)` with `a` kept live silently grew `a` on the
// self-host IR path (22 expected, 23 observed). The port threads may-grow
// flags ('4') through fn_param_sigs and brackets surviving ident args at
// call sites with __fern_rc_inc/dec, exempting the dying self-reassign /
// return-position shapes so accumulator chains stay O(n).
//
// The struct / nested-field shapes were ALREADY correct on the self-host
// (their field-read appends route through the clone-form
// lower_arr_append_value) — pinned here as regression guards alongside the
// bare-param fixes. Exit codes cross-checked against native -interp.
var selfHostAppendBorrowedCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare-param", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = grow(a, 1);\n    a = grow(a, 2);\n    var before: i32 = a.len();\n    var c: i32[] = grow(a, 3);\n    var after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
	{"transitive", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction via(xs: i32[], x: i32): i32[] {\n    return grow(xs, x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = via(a, 1);\n    a = via(a, 2);\n    var before: i32 = a.len();\n    var c: i32[] = via(a, 3);\n    var after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
	{"return-borrow", "function tail(acc: i32[], x: i32): i32[] {\n    return acc.append(x);\n}\nfunction caller(acc: i32[]): i32 {\n    var r = tail(acc, 9);\n    return r.len() * 10 + acc.len();\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = a.append(1);\n    a = a.append(2);\n    return caller(a) + 10;\n}", 42},
	{"recursive-acc", "function walk(acc: i32[], n: i32): i32[] {\n    if (n == 0) { return acc; }\n    return walk(acc.append(n), n - 1);\n}\nfunction main(): i32 {\n    var out = walk([], 40);\n    return out.len() + 2;\n}", 42},
	{"struct-field-regress", "struct Box { xs: i32[] }\nfunction (b: Box) push(x: i32): Box {\n    var ys: i32[] = b.xs.append(x);\n    return Box { xs: ys };\n}\nfunction main(): i32 {\n    var a: Box = Box { xs: [] };\n    a = a.push(1);\n    a = a.push(2);\n    var before: i32 = a.xs.len();\n    var c: Box = a.push(3);\n    var after: i32 = a.xs.len();\n    return before * 10 + after;\n}", 22},
	{"selfreassign-chain", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    var i: i32 = 0;\n    while (i < 30) { a = grow(a, i); i = i + 1; }\n    return a.len() + 12;\n}", 42},
}

// TestSelfHostAppendBorrowedParamX86_64 — the containment through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostAppendBorrowedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
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

// TestSelfHostAppendBorrowedParamArm64 — CI-gated arm64 counterpart; the
// containment is shared irlower analysis, so both register backends inherit.
func TestSelfHostAppendBorrowedParamArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_arm64.fern", "asm.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
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
