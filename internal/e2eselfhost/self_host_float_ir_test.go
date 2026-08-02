package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// floatMathIRCases exercise std/float's f64 math builtins — __sqrt_f64 /
// __floor_f64 / __ceil_f64 / __trunc_f64 / __abs_f64 / __round_f64 — through the
// self-host IR path on x86-64 + wasm. Most map to one hardware instruction on
// every backend (sqrtsd / roundsd / sign-mask on x86, fsqrt / frint* / fabs on
// arm64, f64.sqrt / floor / ceil / trunc / abs on wasm), so the IR path now
// lowers them (op_funary) instead of falling back to the AST emitter — the
// FEATURE-AUDIT "float math builtins" row was self-host-blank.
//
// __round_f64 is round-half-away-from-zero (Go's math.Round): one instruction on
// arm64 (frinta) but emulated as trunc(x+copysign(0.5,x)) on x86 (roundsd has no
// ties-away mode) and wasm (f64.nearest is ties-to-EVEN). The half-integer cases
// below (2.5 -> 3, 99.5 -> 100) are exactly where ties-to-even would diverge.
//
// These cases pin routing to the "ir" path via the pathprobe driver. (The older
// combined test used `-ir`, which silently falls back to AST when the module is
// not all_eligible, so it never verified the IR path.) The libm transcendentals
// (log/exp/sin/cos/pow) remain on the AST path — a documented follow-up.
//
// Each case casts its f64 result to i32 and returns a non-negative value kept
// <= 126 (the wasmtime exit-code truncation gap, cf. #2908), oracle-checked
// against the interpreter. FEATURE-AUDIT std/float row.
var floatMathIRCases = []struct {
	name string
	main string
}{
	{"sqrt", `return __sqrt_f64(16.0) as i32;`},
	{"sqrt-large", `return __sqrt_f64(10000.0) as i32;`},
	{"floor", `return __floor_f64(7.8) as i32;`},
	{"ceil", `return __ceil_f64(7.2) as i32;`},
	{"trunc", `return __trunc_f64(7.9) as i32;`},
	// abs of a negative input: exercises the sign-bit clear with a positive result.
	{"abs", `return __abs_f64(0.0 - 5.5) as i32;`},
	// round half-away: 2.5 -> 3 (ties-to-even would give 2), 99.5 -> 100.
	{"round-half", `return __round_f64(2.5) as i32;`},
	{"round-half-large", `return __round_f64(99.5) as i32;`},
	// round below / above the half: 2.4 -> 2, 2.6 -> 3.
	{"round-down", `return __round_f64(2.4) as i32;`},
	{"round-up", `return __round_f64(2.6) as i32;`},
	// Nested intrinsics: sqrt(abs(-16)) = 4.
	{"nested", `return __sqrt_f64(__abs_f64(0.0 - 16.0)) as i32;`},
	// Intrinsic result feeding f64 arithmetic: sqrt(2) * 10 = 14.14 -> 14.
	{"in-expr", `var x: f64 = 2.0; return (__sqrt_f64(x) * 10.0) as i32;`},
	// f64 local round-trip: floor of a stored value.
	{"via-local", `var y: f64 = 9.99; return __floor_f64(y) as i32;`},
	// round of an f64 local.
	{"round-via-local", `var z: f64 = 4.5; return __round_f64(z) as i32;`},
}

func floatMathIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFloatMathIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with routing pinned to the "ir" path.
func TestSelfHostFloatMathIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatMathIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostFloatMathIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFloatMathIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host float-math wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range floatMathIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatMathIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "float_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("float-math wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
