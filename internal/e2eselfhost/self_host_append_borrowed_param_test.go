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
	// #6008: the containment's blind spot was the BINDING, not the call. A
	// self-reassign call (`a = bind(a)`) is grow-exempt, so `bind`'s param
	// arrives unbracketed at rc==1 — and `var q: i32[] = p.append(99)` then
	// bound q straight to the buffer arr_push had appended into IN PLACE.
	// One buffer, two names, each with a release obligation: `q = leaf(q)`
	// reallocates, so bind's cow-guarded reassign-dec frees it, and main's
	// frees it again. `a` is 5 long either way; __rc_underflow() is 0 only
	// when the accounting balances (1 before the fix, so 51 vs 50). In
	// std/regex this freed an ISplit payload that `caps.with(slot, ti)` then
	// recycled, and the Pike VM branched to prog[-1].
	{"binding-aliases-param", "function leaf(p: i32[]): i32[] {\n    return p.append(3);\n}\nfunction bind(p: i32[]): i32[] {\n    var q: i32[] = p.append(99);\n    q = leaf(q);\n    return q;\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = a.append(1);\n    a = a.append(2);\n    a = a.append(3);\n    a = bind(a);\n    return a.len() * 10 + __rc_underflow();\n}", 50},
	// The chained-tx corruption shape (reduced from the WIT codec): a
	// self-appending may-grow callee chaining into further may-grow
	// callees, looped over a reused empty out. p ends at 12 → 60, and
	// __rc_underflow() must be 0 (the +1 accounting balances exactly).
	{"chained-tx-underflow", "struct TxR2 { out: i32[], next: i32 }\nfunction inner(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    return TxR2 { out: out, next: p + 1 };\n}\nfunction outer(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    var ne: TxR2 = inner(buf, p + 1, out);\n    out = ne.out;\n    return inner(buf, ne.next, out);\n}\nfunction main(): i32 {\n    var buf: i32[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];\n    var none: i32[] = [];\n    var p: i32 = 0;\n    var i: i32 = 0;\n    while (i < 4) {\n        var skip: TxR2 = outer(buf, p, none);\n        p = skip.next;\n        i = i + 1;\n    }\n    return p * 5 + (__rc_underflow() % 5);\n}", 60},
}

// TestSelfHostAppendBorrowedParamX86_64 — the containment through the
// PRODUCTION x86-64 IR path (asm_ir_run `-ir`).
func TestSelfHostAppendBorrowedParamX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
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
	copySelfHostFiles(t, dir, "util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux", "-ir")
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
