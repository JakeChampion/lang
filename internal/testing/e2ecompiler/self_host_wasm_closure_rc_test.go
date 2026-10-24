package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcClosureWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("no wasmtime")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"clos-box-freed", "function main(): i32 { let n: i32 = 5; let f = (x: i32): i32 => { return x + n; }; return f(37) + __rc_underflow_count(); }", 42},
		{"clos-multi", "function main(): i32 { let a: i32 = 30; let b: i32 = 12; let f = (): i32 => { return a + b; }; return f() + __rc_underflow_count(); }", 42},
		{"clos-string-cap", "function main(): i32 { let s: string = \"hello\"; let f = (): i32 => { return s.len(); }; return f() + 37 + __rc_underflow_count(); }", 42},
		{"clos-scalar-churn", "function mk(k: i32): i32 { let n: i32 = k; let f = (x: i32): i32 => { return x + n; }; return f(1); } function main(): i32 { let s = 0; let k = 0; while (k < 200000) { s = mk(k); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 3},
		{"clos-return", "function adder(a: i32): (i32) => i32 { let f = (b: i32): i32 => { return a + b; }; return f; } function main(): i32 { let add10 = adder(10); let add20 = adder(20); return add10(5) + add20(7) + __rc_underflow_count(); }", 42},
		{"cap-string-released", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let f = (): i32 => { return s.len(); }; return f() + 38 + __rc_underflow_count(); }", 42},
		{"cap-string-churn", "function mk(): i32 { let s: string = \"ab\" + \"cd\"; let f = (): i32 => { return s.len(); }; return f(); } function main(): i32 { let k = 0; let n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 4},
		{"cap-array-churn", "function mk(): i32 { let xs: i32[] = [10, 20, 30]; let f = (): i32 => { return xs[1]; }; return f(); } function main(): i32 { let k = 0; let n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 6},
		{"cap-struct-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { let i = Inner { xs: [1, 2, 3, 4], n: 5 }; let f = (): i32 => { return i.n; }; return f(); } function main(): i32 { let k = 0; let n = 0; while (k < 200000) { n = mk(); k = k + 1; } return (n % 7) + __rc_underflow_count(); }", 5},
		{"cap-mixed", "function main(): i32 { let s: string = \"xy\" + \"zw\"; let a: i32 = 38; let f = (): i32 => { return s.len() + a; }; return f() + __rc_underflow_count(); }", 42},
		{"cap-structarr-val", "struct Inner { xs: i32[], n: i32 } function main(): i32 { let ps: Inner[] = [Inner { xs: [1, 2], n: 40 }, Inner { xs: [3, 4], n: 9 }]; let f = (): i32 => { return ps[0].n + ps[1].xs[1]; }; return f() + __rc_underflow_count(); }", 44},
		{"cap-structarr-churn", "struct Inner { xs: i32[], n: i32 } function mk(): i32 { let ps: Inner[] = [Inner { xs: [1, 2, 3], n: 4 }, Inner { xs: [5, 6, 7], n: 8 }]; let f = (): i32 => { return ps[0].n; }; return f(); } function main(): i32 { let k = 0; let s = 0; while (k < 200000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			os.WriteFile(watPath, wat, 0o644)
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: exited %d want %d\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
