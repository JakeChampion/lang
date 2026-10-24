package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcArrElemWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm arr-elem-rc e2e")
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
		{"str-array-elem-released", "function main(): i32 { let a: string = \"a\" + \"x\"; let b: string = \"b\" + \"y\"; let arr: string[] = [a, b]; return arr[0].len() + arr[1].len() + __rc_underflow_count(); }", 4},
		{"str-array-elem-aliased-clean", "function main(): i32 { let s: string = \"ab\" + \"cd\"; let arr: string[] = [s]; return arr[0].len() + s.len() + __rc_underflow_count(); }", 8},
		{"str-array-append-released", "function main(): i32 { let arr: string[] = []; let i = 0; while (i < 3) { arr = arr.append(\"x\" + \"y\"); i = i + 1; } return arr.len() + arr[2].len() + __rc_underflow_count(); }", 5},
		{"str-array-churn-clean", "function mk(): i32 { let arr: string[] = [\"a\" + \"b\", \"c\" + \"d\", \"e\" + \"f\"]; return arr[0].len() + arr[1].len() + arr[2].len(); } function main(): i32 { let k = 0; let s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		{"struct-array-elem-released", "struct P { x: i32, y: i32 } function main(): i32 { let ps: P[] = [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; return ps[0].x + ps[1].y + __rc_underflow_count(); }", 5},
		{"struct-array-elem-aliased-clean", "struct P { x: i32, y: i32 } function main(): i32 { let p = P { x: 5, y: 6 }; let ps: P[] = [p]; return ps[0].x + p.y + __rc_underflow_count(); }", 11},
		{"str-array-aliased-buffer-clean", "function main(): i32 { let a: string = \"p\" + \"q\"; let xs: string[] = [a]; let ys: string[] = xs; return ys[0].len() + xs[0].len() + __rc_underflow_count(); }", 4},
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
