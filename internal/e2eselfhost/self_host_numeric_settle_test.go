package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An integer literal written in a float position — `var x: f64 = 1;` — is an
// UNSETTLED literal: the destination decides its type. Native resolves that with
// `settleNumeric`; the self-host had no counterpart (#6654), and the cost landed
// twice. The checker read the literal as i32 and REJECTED seven ordinary shapes
// native compiles, and the IR lowerer — which re-infers float-ness bottom-up
// with no destination hint — emitted an INTEGER constant into the float slot, so
// the same programs computed WRONG ANSWERS wherever the checker had not already
// refused them (measured on `1630563`: var-init, return, struct-field and
// array-literal all came back with the else-branch value).
//
// `parser.settle_module` restamps the literal on the AST at the shared parse
// entry, which is what both sides read — so these cases pin the checker AND the
// emitted value, on every self-host backend.
//
// Each program is written so the answer DIFFERS between a settled and an
// unsettled literal: the float comparison it ends in is true only when the
// literal really became a float.
var numericSettleCases = []struct {
	name     string
	src      string
	expected int
}{
	{"var-init",
		`function main(): i32 { var x: f64 = 1; var y: f64 = x + 0.5; if (y > 1.4) { return 3; } return 4; }`, 3},
	{"assignment",
		`function main(): i32 { var x: f64 = 1.0; x = 2; if (x > 1.5) { return 13; } return 14; }`, 13},
	{"return",
		`function g(): f64 { return 1; } function main(): i32 { if (g() > 0.5) { return 5; } return 6; }`, 5},
	{"call-argument",
		`function g(x: f64): f64 { return x + 0.25; } function main(): i32 { if (g(1) > 1.2) { return 7; } return 8; }`, 7},
	{"struct-literal-field",
		`struct S { x: f64 } function main(): i32 { var s: S = S { x: 1 }; if (s.x > 0.5) { return 9; } return 10; }`, 9},
	{"array-literal",
		`function main(): i32 { var xs: f64[] = [1, 2]; if (xs[1] > 1.5) { return 11; } return 12; }`, 11},
	{"tuple-literal",
		`function main(): i32 { var t: (f64, i32) = (1, 2); if (t.0 > 0.5) { return 15; } return 16; }`, 15},
	// Arithmetic over bare literals settles as a unit — native settles the
	// operands of `+ - * /` recursively, so this is float division (3.5), not
	// integer division (3).
	{"literal-arithmetic",
		`function main(): i32 { var x: f64 = 7 / 2; if (x > 3.4) { return 17; } return 18; }`, 17},
	// A negated literal settles through the unary.
	{"negated-literal",
		`function main(): i32 { var x: f64 = 0 - 1; if (x < 0.0 - 0.5) { return 19; } return 20; }`, 19},
}

// numericSettleRejects are the shapes settling must NOT swallow: the literalness
// is load-bearing, so an i32 VALUE in a float slot stays an error, with the same
// code native gives it.
var numericSettleRejects = []struct {
	name string
	src  string
	code string
}{
	{"i32-local-to-f64", `function main(): i32 { var a: i32 = 1; var x: f64 = a; return 0; }`, "E003"},
	{"suffixed-literal", `function main(): i32 { var x: f64 = 1i64; return 0; }`, "E003"},
	{"string-to-f64", `function main(): i32 { var x: f64 = "s"; return 0; }`, "E003"},
	{"float-to-i32", `function main(): i32 { var x: i32 = 1.5; return 0; }`, "E003"},
	{"i32-local-argument", `function g(x: f64): i32 { return 0; } function main(): i32 { var a: i32 = 1; return g(a); }`, "E038"},
}

// TestSelfHostNumericSettleChecker pins the accept side: every case checks clean
// on the self-host, with the native interpreter as the oracle for the value.
func TestSelfHostNumericSettleChecker(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)
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

// TestSelfHostNumericSettleRejects pins the reject side, so the settle stays
// literal-shaped rather than becoming a blanket i32→f64 assignability rule.
func TestSelfHostNumericSettleRejects(t *testing.T) {
	checkerBin, runner, _ := buildCheckerDriverBin(t, "checker_run.fern", false)
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

// TestSelfHostNumericSettleIRX86_64 pins the emitted VALUE on the x86-64 IR
// path — the half of #6654 that was a wrong answer rather than a refusal.
func TestSelfHostNumericSettleIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

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

// TestSelfHostNumericSettleIRWasm is the stack-machine backend's copy: settling
// happens on the AST, so every backend inherits it, and this proves it.
func TestSelfHostNumericSettleIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host numeric-settle wasm IR e2e")
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

	for _, tc := range numericSettleCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
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

// runSelfHostChecker feeds `src` to a built self-hosted checker binary and
// returns its exit code plus stderr (the formatted diagnostics).
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
