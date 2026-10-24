package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var numericSettleCases = []struct {
	name     string
	src      string
	expected int
}{
	{"var-init",
		`function main(): i32 { let x: f64 = 1; let y: f64 = x + 0.5; if (y > 1.4) { return 3; } return 4; }`, 3},
	{"assignment",
		`function main(): i32 { let x: f64 = 1.0; x = 2; if (x > 1.5) { return 13; } return 14; }`, 13},
	{"return",
		`function g(): f64 { return 1; } function main(): i32 { if (g() > 0.5) { return 5; } return 6; }`, 5},
	{"call-argument",
		`function g(x: f64): f64 { return x + 0.25; } function main(): i32 { if (g(1) > 1.2) { return 7; } return 8; }`, 7},
	{"struct-literal-field",
		`struct S { x: f64 } function main(): i32 { let s: S = S { x: 1 }; if (s.x > 0.5) { return 9; } return 10; }`, 9},
	{"array-literal",
		`function main(): i32 { let xs: f64[] = [1, 2]; if (xs[1] > 1.5) { return 11; } return 12; }`, 11},
	{"tuple-literal",
		`function main(): i32 { let t: (f64, i32) = (1, 2); if (t.0 > 0.5) { return 15; } return 16; }`, 15},
	{"literal-arithmetic",
		`function main(): i32 { let x: f64 = 7 / 2; if (x > 3.4) { return 17; } return 18; }`, 17},
	{"negated-literal",
		`function main(): i32 { let x: f64 = 0 - 1; if (x < 0.0 - 0.5) { return 19; } return 20; }`, 19},
	{"value-if",
		`function main(): i32 { let x: f64 = if (1 < 2) { 1 } else { 2 }; let y: f64 = x + 0.5; if (y > 1.4) { return 21; } return 22; }`, 21},
	{"value-if-else-branch",
		`function main(): i32 { let x: f64 = if (1 > 2) { 1 } else { 3 }; let y: f64 = x + 0.5; if (y > 3.4) { return 23; } return 24; }`, 23},
	{"value-match",
		`enum E { A, B } function main(): i32 { let e: E = B; let x: f64 = match (e) { A => 1, B => 3 }; let y: f64 = x + 0.5; if (y > 3.4) { return 25; } return 26; }`, 25},
	{"value-if-return",
		`function g(): f64 { return if (1 < 2) { 1 } else { 2 }; } function main(): i32 { if (g() > 0.5) { return 27; } return 28; }`, 27},
	{"value-else-if-chain",
		`function main(): i32 { let x: f64 = if (1 > 2) { 1 } else if (2 < 3) { 3 } else { 5 }; let y: f64 = x + 0.5; if (y > 3.4) { return 29; } return 30; }`, 29},
	{"value-if-block-tail",
		`function main(): i32 { let x: f64 = if (1 < 2) { let q: i32 = 7; 1 } else { 2 }; let y: f64 = x + 0.5; if (y > 1.4) { return 31; } return 32; }`, 31},
	{"value-if-nested",
		`function main(): i32 { let x: f64 = if (1 < 2) { if (2 < 3) { 1 } else { 2 } } else { 3 }; let y: f64 = x + 0.5; if (y > 1.4) { return 33; } return 34; }`, 33},
	{"value-if-arithmetic-arm",
		`function main(): i32 { let x: f64 = if (1 < 2) { 7 / 2 } else { 1 }; if (x > 3.4) { return 35; } return 36; }`, 35},
	{"value-if-call-argument",
		`function g(v: f64): f64 { return v + 0.25; } function main(): i32 { if (g(if (1 < 2) { 1 } else { 2 }) > 1.2) { return 37; } return 38; }`, 37},
	{"binary-operand-f64",
		`function main(): i32 { let x: f64 = 100.5f64; let y: f64 = x - 100; if (y == 0.5f64) { return 39; } return 40; }`, 39},
	{"binary-operand-f32",
		`function main(): i32 { let r: f32 = 1.5f32; let s: f32 = r * 2; if (s == 3.0f32) { return 41; } return 42; }`, 41},
	{"binary-left-literal",
		`function main(): i32 { let r: f64 = 0.25f64; let s: f64 = 1 - r; if (s > 0.7f64) { return 43; } return 44; }`, 43},
	{"binary-guard-compare",
		`enum Shape { Circle(f32), Square(f32) } function classify(s: Shape): i32 { match (s) { Circle(r) when r < 2 => { return 1; }, Circle(_) => { return 2; }, Square(_) => { return 3; } } return 0; } function main(): i32 { if (classify(Circle(1.5f32)) != 1) { return 46; } if (classify(Circle(2.5f32)) != 2) { return 47; } return 45; }`, 45},
}

var numericSettleRejects = []struct {
	name string
	src  string
	code string
}{
	{"i32-local-to-f64", `function main(): i32 { let a: i32 = 1; let x: f64 = a; return 0; }`, "E003"},
	{"suffixed-literal", `function main(): i32 { let x: f64 = 1i64; return 0; }`, "E003"},
	{"string-to-f64", `function main(): i32 { let x: f64 = "s"; return 0; }`, "E003"},
	{"float-to-i32", `function main(): i32 { let x: i32 = 1.5; return 0; }`, "E003"},
	{"i32-local-argument", `function g(x: f64): i32 { return 0; } function main(): i32 { let a: i32 = 1; return g(a); }`, "E038"},
	{"mixed-binary", `function main(): i32 { let f: f64 = 1 + 2.5; return 0; }`, "E009"},
	{"mixed-binary-reversed", `function main(): i32 { let f: f64 = 2.5 + 1; return 0; }`, "E009"},
	{"binary-i32-local-operand", `function main(): i32 { let n: i32 = 2; let r: f64 = 1.5f64; let s: f64 = r * n; return 0; }`, "E009"},
	{"value-if-i32-local-arm",
		`function main(): i32 { let n: i32 = 1; let x: f64 = if (1 < 2) { n } else { 2 }; return 0; }`, "E003"},
	{"value-if-string-arm",
		`function main(): i32 { let x: f64 = if (1 < 2) { 1 } else { "s" }; return 0; }`, "E031"},
	{"value-match-string-arm",
		`enum E { A, B } function main(): i32 { let e: E = A; let x: f64 = match (e) { A => 1, B => "s" }; return 0; }`, "E031"},
	{"value-if-mixed-binary-arm",
		`function main(): i32 { let x: f64 = if (1 < 2) { 1 + 2.5 } else { 1 }; return 0; }`, "E009"},
}

func TestSelfHostNumericSettleChecker(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)
	for _, tc := range numericSettleCases {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src)
			if code != 0 {
				t.Fatalf("self-host checker exited %d, want 0\nstderr: %s", code, stderr)
			}
			if diag := strings.TrimSpace(stderr); diag != "" {
				t.Errorf("self-host checker reported %q, want no diagnostic", diag)
			}
			if got := runInterpExit(t, tc.src); got != tc.expected {
				t.Errorf("native oracle = %d, want %d — the case no longer discriminates", got, tc.expected)
			}
		})
	}
}

func TestSelfHostNumericSettleRejects(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "drivers/checker_run.fern", false)
	for _, tc := range numericSettleRejects {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := runSelfHostChecker(t, checkerBin, runner, tc.src)
			if code == 0 {
				t.Fatalf("self-host checker accepted %q, want a %s rejection", tc.src, tc.code)
			}
			if !strings.Contains(stderr, tc.code) {
				t.Errorf("self-host checker stderr missing %s:\n%s", tc.code, stderr)
			}
		})
	}
}

func TestSelfHostNumericSettleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_run.fern", "drivers/asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range numericSettleCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func TestSelfHostNumericSettleIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host numeric-settle wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range numericSettleCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("numeric-settle wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

func runSelfHostChecker(t *testing.T, bin string, runner []string, src string) (int, string) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), stderr.String()
}
