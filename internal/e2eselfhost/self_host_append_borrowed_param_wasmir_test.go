package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostAppendBorrowedParamWasmIR — the #4873 caller-side may-grow
// containment on the self-host WASM-IR backend (#5325, re-landing the
// reverted #5138). Three pieces:
//
//   - the rc-uniqueness gate in $__fern_arr_push (arr_push_helper): in-place
//     append only for a sole-owner (rc==1) or immortal (bit-31) receiver;
//     a shared receiver takes the copy path (un-share copies keep the SAME
//     cap — the #3425 arena lesson);
//   - the caller-side share bracket wired to the register-backend pair
//     (share_inc → $__fern_rc_inc, share_dec → the freeing $__fern_arr_dec)
//     instead of the historical no-op (whose "arrays are headerless" premise
//     was stale — wasm-IR arrays are rc-headered via $__fern_arr_box);
//   - the root-cause fix that blocked #5138: $__fern_arr_push_owned frees
//     the superseded old buffer ONLY when it was the sole owner (rc==1),
//     mirroring asm_ir's defensive "not sole owner — leave" gate. The old
//     unconditional delegation to the DECREMENTING $__fern_arr_dec ate the
//     bracket's +1 on a bracketed shared receiver, so the bracket's own dec
//     freed the caller's still-referenced buffer — the WIT-codec SIGABRT.
//
// Cases: the six register-suite shapes (self_host_append_borrowed_param_test)
// plus the chained-tx shape reduced from the WIT codec (a may-grow callee
// that self-appends its param then passes it through further may-grow
// callees, looped over a reused empty array — 2 iterations were the minimal
// corruption; the trailing __rc_underflow() read asserts the rc accounting
// balances exactly, not merely that the heap survived).
func TestSelfHostAppendBorrowedParamWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR borrowed-param e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		want int
	}{
		{"bare-param", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = grow(a, 1);\n    a = grow(a, 2);\n    var before: i32 = a.len();\n    var c: i32[] = grow(a, 3);\n    var after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
		{"transitive", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction via(xs: i32[], x: i32): i32[] {\n    return grow(xs, x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = via(a, 1);\n    a = via(a, 2);\n    var before: i32 = a.len();\n    var c: i32[] = via(a, 3);\n    var after: i32 = a.len();\n    return before * 10 + after;\n}", 22},
		{"return-borrow", "function tail(acc: i32[], x: i32): i32[] {\n    return acc.append(x);\n}\nfunction caller(acc: i32[]): i32 {\n    var r = tail(acc, 9);\n    return r.len() * 10 + acc.len();\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    a = a.append(1);\n    a = a.append(2);\n    return caller(a) + 10;\n}", 42},
		{"recursive-acc", "function walk(acc: i32[], n: i32): i32[] {\n    if (n == 0) { return acc; }\n    return walk(acc.append(n), n - 1);\n}\nfunction main(): i32 {\n    var out = walk([], 40);\n    return out.len() + 2;\n}", 42},
		{"struct-field-regress", "struct Box { xs: i32[] }\nfunction (b: Box) push(x: i32): Box {\n    var ys: i32[] = b.xs.append(x);\n    return Box { xs: ys };\n}\nfunction main(): i32 {\n    var a: Box = Box { xs: [] };\n    a = a.push(1);\n    a = a.push(2);\n    var before: i32 = a.xs.len();\n    var c: Box = a.push(3);\n    var after: i32 = a.xs.len();\n    return before * 10 + after;\n}", 22},
		{"selfreassign-chain", "function grow(xs: i32[], x: i32): i32[] {\n    return xs.append(x);\n}\nfunction main(): i32 {\n    var a: i32[] = [];\n    var i: i32 = 0;\n    while (i < 30) { a = grow(a, i); i = i + 1; }\n    return a.len() + 12;\n}", 42},
		// The chained-tx corruption shape (reduced from the WIT codec): a
		// self-appending may-grow callee chaining into further may-grow
		// callees, looped over a reused empty out. p ends at 12 → 60, and
		// __rc_underflow() must be 0 (the +1 accounting balances exactly).
		{"chained-tx-underflow", "struct TxR2 { out: i32[], next: i32 }\nfunction inner(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    return TxR2 { out: out, next: p + 1 };\n}\nfunction outer(buf: i32[], p: i32, out: i32[]): TxR2 {\n    out = out.append(buf[p]);\n    var ne: TxR2 = inner(buf, p + 1, out);\n    out = ne.out;\n    return inner(buf, ne.next, out);\n}\nfunction main(): i32 {\n    var buf: i32[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12];\n    var none: i32[] = [];\n    var p: i32 = 0;\n    var i: i32 = 0;\n    while (i < 4) {\n        var skip: TxR2 = outer(buf, p, none);\n        p = skip.next;\n        i = i + 1;\n    }\n    return p * 5 + (__rc_underflow() % 5);\n}", 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "bp_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
