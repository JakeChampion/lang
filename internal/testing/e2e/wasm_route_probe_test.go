package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWasmRouteProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const passthrough = "function pick2[T](a: T, b: T, useA: boolean): T { if (useA) { return a; } return b; }\n" +
		"function main(): i32 {\n" +
		"    let x: i64 = 5000000000;\n" +
		"    let y: i64 = 7;\n" +
		"    let z: i64 = pick2(x, y, false);\n" +
		"    if (z == 7) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	const usesTypevar = "function eqf[T](a: T, b: T): boolean { return a == b; }\n" +
		"function main(): i32 {\n" +
		"    let x: i64 = 5000000000;\n" +
		"    let y: i64 = 5000000000;\n" +
		"    if (eqf(x, y)) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	const twoTypevars = "function both[T, U](a: T, b: U): boolean { return a == a && b == b; }\n" +
		"function main(): i32 {\n" +
		"    let x: i64 = 5000000000;\n" +
		"    let y: i64 = 5000000000;\n" +
		"    if (both(x, y)) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	const foldShape = "function sum_all[T](xs: T[], seed: T): T { let acc: T = seed; for x in xs { acc = x; } return acc; }\n" +
		"function main(): i32 {\n" +
		"    let xs: i64[] = [1, 2, 5000000000];\n" +
		"    let s: i64 = sum_all(xs, 0 as i64);\n" +
		"    if (s == 5000000000) { return 42; }\n" +
		"    return 1;\n" +
		"}\n"
	const twoVarArrayShape = "function map2[T, U](xs: T[], f: (T) => U): U[] {\n" +
		"    let out: U[] = [];\n" +
		"    for x in xs { out = out.append(f(x)); }\n" +
		"    return out;\n" +
		"}\n" +
		"function half(x: i64): i32 { if (x > 1000000000) { return 42; } return 1; }\n" +
		"function main(): i32 {\n" +
		"    let xs: i64[] = [5000000000];\n" +
		"    let ys: i32[] = map2(xs, half);\n" +
		"    return ys[0];\n" +
		"}\n"

	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{"plain", "function main(): i32 { return 42; }\n", "ir"},
		{"struct-and-array", "struct P { x: i32, y: i32 }\nfunction main(): i32 { let p = P { x: 40, y: 2 }; let a = [p.x, p.y]; return a[0] + a[1]; }\n", "ir"},
		{"erased-wide-passthrough", passthrough, "ir"},
		{"erased-wide-uses-typevar", usesTypevar, "ir"},
		{"erased-wide-two-typevars", twoTypevars, "ir"},
		{"erased-wide-fold-shape", foldShape, "ir"},
		{"erased-wide-two-var-array", twoVarArrayShape, "ir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(string(runCaptureArgs(t, runner, driverBin, []byte(tc.src), "-decide")))
			if got != tc.want {
				t.Errorf("-decide = %q, want %q", got, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name string
		src  string
	}{
		{"ir-route-runs", passthrough},
		{"erased-wide-uses-typevar-runs", usesTypevar},
		{"erased-wide-two-typevars-runs", twoTypevars},
		{"erased-wide-fold-shape-runs", foldShape},
		{"erased-wide-two-var-array-runs", twoVarArrayShape},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("wasmtime"); err != nil {
				t.Skip("wasmtime not on PATH")
			}
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != 42 {
				t.Errorf("wasm exited %d, want 42", code)
			}
		})
	}
}

func runCaptureArgs(t *testing.T, runner []string, bin string, stdin []byte, args ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, args...)
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), bin), args...)...)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("driver: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.Bytes()
}
