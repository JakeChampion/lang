package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcTupleBoxWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm tuple-box e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"tuple-values-intact", "function main(): i32 { let t = (30, 12); return t.0 + t.1; }", 42},
		{"tuple-holds-array", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let t = (xs, 9); return t.0[2] + t.1 + __rc_underflow_count(); }", 12},
		{"tuple-destructure", "function main(): i32 { let t = (8, 34); let (a, b) = t; return a + b; }", 42},
		{"tuple-swept-clean", "function main(): i32 { let t = (5, 7); return t.0 + t.1 + __rc_underflow_count(); }", 12},
		{"tuple-alias-clean", "function main(): i32 { let t = (3, 4); let u = t; return u.0 + t.1 + __rc_underflow_count(); }", 7},
		{"tuple-loop-clean", "function main(): i32 { let s = 0; let k = 0; while (k < 1000) { let t = (k, 2); s = s + t.1; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
		{"tuple-array-elem-released", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let t = (xs, 9); return t.0[2] + t.1 + __rc_underflow_count(); }", 12},
		{"tuple-string-elem-released", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let t = (s, 5); return t.0.len() + t.1 + __rc_underflow_count(); }", 9},
		{"tuple-array-churn-clean", "function mk(): i32 { let a: i32[] = [1, 2, 3, 4, 5, 6, 7, 8]; let t = (a, 5); return t.0[7] + t.1; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		{"tuple-strarray-elem-released", "function main(): i32 { let strs: string[] = [\"a\" + \"b\", \"c\" + \"d\"]; let t = (strs, 5); return t.0[0].len() + t.1 + __rc_underflow_count(); }", 7},
		{"tuple-i32array-flat-safe", "function main(): i32 { let ns: i32[] = [262184, 262192, 262200]; let t = (ns, 4); return t.0.len() + t.1 + __rc_underflow_count(); }", 7},
		{"tuple-strarray-churn-clean", "function mk(): i32 { let t = ([\"x\" + \"y\", \"z\" + \"w\"], 3); return t.0[0].len() + t.1; } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
