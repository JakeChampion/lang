package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// floatNanIRCases pin IEEE-754 NaN comparison semantics on the self-host IR
// path. A NaN is produced importlessly with `0.0 / 0.0`; every ordered
// comparison against it must be false (`< > <= >=` and `==`), and only `!=`
// must be true — including `NaN != NaN`. This is the subtle half of float
// comparison: an x86-64 `ucomisd` sets the parity flag on an unordered
// (NaN) operand, so the `setcc` sequence the IR backend emits has to fold
// parity in correctly (e.g. `==` must stay false when PF=1, `!=` true);
// wasm's `f64.eq`/`f64.ne`/`f64.lt`/… already follow IEEE directly. Ordered
// (non-NaN) cases are included as a sanity floor. Each case returns a small
// deterministic int, pinned to the `"ir"` path; expectations verified against
// the native interpreter. FEATURE-AUDIT "Float comparison + NaN semantics" row.
var floatNanIRCases = []struct {
	name string
	main string
	want int
}{
	// NaN != NaN is the one true comparison.
	{"nan-ne-self", `var n: f64 = 0.0 / 0.0; if (n != n) { return 1; } return 0;`, 1},
	// NaN == NaN is false.
	{"nan-eq-self", `var n: f64 = 0.0 / 0.0; if (n == n) { return 1; } return 0;`, 0},
	// every ordered comparison with NaN is false.
	{"nan-lt", `var n: f64 = 0.0 / 0.0; if (n < 1.0) { return 1; } return 0;`, 0},
	{"nan-gt", `var n: f64 = 0.0 / 0.0; if (n > 1.0) { return 1; } return 0;`, 0},
	{"nan-ge", `var n: f64 = 0.0 / 0.0; if (n >= 1.0) { return 1; } return 0;`, 0},
	{"nan-le", `var n: f64 = 0.0 / 0.0; if (n <= 1.0) { return 1; } return 0;`, 0},
	// the negation: !(NaN < 1.0) is true (the else path runs).
	{"nan-lt-negated", `var n: f64 = 0.0 / 0.0; if (n < 1.0) { return 0; } return 9;`, 9},
	// ordered (non-NaN) sanity: a real f64 compares normally.
	{"ordered-eq", `var a: f64 = 1.5; if (a == a) { return 7; } return 0;`, 7},
	{"ordered-lt", `if (1.0 < 2.0) { return 5; } return 0;`, 5},
}

func floatNanIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostFloatNanIRX86_64 routes each case through the self-hosted x86-64
// IR driver, pinned to the "ir" path. The x86-64 backend is the interesting
// one: its float compare must fold the unordered (parity) flag correctly.
func TestSelfHostFloatNanIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range floatNanIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatNanIRSrc(tc.main))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFloatNanIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostFloatNanIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host float-nan wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range floatNanIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(floatNanIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "floatnan_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("float-nan wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
