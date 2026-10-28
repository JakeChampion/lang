package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostRcRuntimeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm RC e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	for _, tc := range rcRuntimeCases {
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

func TestSelfHostRcFreeWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC free e2e")
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
		{"reclaim-churn", "function work(n: i32): i32 { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs[n - 1]; } function main(): i32 { let k = 0; let s = 0; while (k < 100000) { s = work(64); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 0},
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

func TestSelfHostRcCallResultWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC call-result e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const genFn = "function gen(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } "
	cases := []struct {
		name string
		src  string
		exit int
	}{
		{"callresult-balanced", genFn + "function main(): i32 { let a: i32[] = gen(5); let b: i32[] = gen(7); return a[4] + b[6] + __rc_underflow_count(); }", 10},
		{"callresult-aliased", genFn + "function main(): i32 { let a: i32[] = gen(5); let c = a; return a[0] + c[4] + __rc_underflow_count(); }", 4},
		{"callresult-borrowed-return", "@noinline function pick(xs: i32[]): i32[] { return xs; } function main(): i32 { let src: i32[] = [1, 2, 3]; let got: i32[] = pick(src); return got[1] + src[0] + __rc_underflow_count(); }", 3},
		{"self-append-not-double-freed", "function main(): i32 { let xs: i32[] = [1, 2]; let ys = xs.append(3); return ys[2] + __rc_underflow_count(); }", 3},
		{"freshbuiltin-random-swept", "function main(): i32 { let b: u8[] = random_bytes(4); return b.len() + __rc_underflow_count(); }", 4},
		{"method-result-swept", "struct Box { n: i32 } function (b: Box) make(): i32[] { let xs: i32[] = []; let i = 0; while (i < b.n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let box = Box { n: 5 }; let r: i32[] = box.make(); return r[4] + __rc_underflow_count(); }", 4},
		{"method-result-loop", "struct Box { n: i32 } function (b: Box) make(): i32[] { let xs: i32[] = []; let i = 0; while (i < b.n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let box = Box { n: 6 }; let s = 0; let k = 0; while (k < 50) { let r: i32[] = box.make(); s = s + r[5]; k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 5},
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

func TestSelfHostRcConstructWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC construction e2e")
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
		{"struct-field-fresh-move", "struct H { items: i32[] } function main(): i32 { let h = H { items: [9, 8, 7] }; return h.items[0] + __rc_underflow_count(); }", 9},
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

func TestSelfHostRcCountingWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm RC counting e2e")
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
		{"alias-balanced", "function main(): i32 { let xs: i32[] = [1, 2, 3]; let ys = xs; return ys[1] + xs[0] + __rc_underflow_count(); }", 3},
		{"multi-alias-balanced", "function main(): i32 { let xs: i32[] = [10, 20]; let a = xs; let b = xs; return a[0] + b[1] + __rc_underflow_count(); }", 30},
		{"append-loop-balanced", "function main(): i32 { let xs: i32[] = []; let i = 0; while (i < 10) { xs = xs.append(i); i = i + 1; } return xs[9] + __rc_underflow_count(); }", 9},
		{"calls-balanced", "function f(): i32 { let xs: i32[] = [1, 2, 3]; let ys = xs; return ys[0]; } function main(): i32 { let r = f(); let s = f(); return r + s + __rc_underflow_count(); }", 2},
		{"borrowed-param-balanced", "function g(a: i32[]): i32 { let b = a; return b[0]; } function main(): i32 { let xs: i32[] = [7, 8]; let r = g(xs); return r + xs[0] + __rc_underflow_count(); }", 14},
		{"branch-local-balanced", "function main(): i32 { let xs: i32[] = [5, 6]; if (xs[0] > 100) { let ys: i32[] = [1, 2]; return ys[0] + __rc_underflow_count(); } return xs[1] + __rc_underflow_count(); }", 6},
		{"loop-local-rebind-clean", "function gen(n: i32): i32[] { let xs: i32[] = []; let i = 0; while (i < n) { xs = xs.append(i); i = i + 1; } return xs; } function main(): i32 { let s = 0; let k = 0; while (k < 1000) { let r: i32[] = gen(8); s = s + r[7]; k = k + 1; } return (s % 100) + __rc_underflow_count(); }", 0},
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

func TestSelfHostRcArrayLayoutWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm array-layout RC e2e")
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
		{"elems-intact-after-layout", "function main(): i32 { let xs: i32[] = [7, 8, 9]; return xs[0] + xs[2] + xs.len(); }", 19},
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
