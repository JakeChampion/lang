package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// numericEdgeIRCases pin the integer/float codegen edge cases on the self-host
// IR path that used to miscompile or trap:
//
//   - #4329 div/rem by zero must NOT raise #DE (SIGFPE): x/0 = 0, x%0 = x, and
//     INT_MIN/-1 = INT_MIN, INT_MIN%-1 = 0 (the x86 idivq faults on both).
//   - #4330 i32 shift counts mask mod 32, not mod 64 (the 64-bit shlq/sarq/shrq
//     masked %cl to 6 bits, so a count >= 32 wrapped wrongly).
//   - #4333 unary f64 `-x` flips the sign bit (op_funary "fneg"), preserving
//     -0.0; the old `0.0 - x` lowering flushed -0.0 to +0.0.
//   - #4332 f64 -> int is saturating: NaN -> 0, overflow clamps to INT_MIN/MAX
//     (x86 cvttsd2si wraps to a sentinel, wasm i32.trunc_f64_s TRAPS on NaN).
//
// Each case returns a small deterministic int, is pinned to the "ir" path on
// x86, and its expectation is the native-interpreter result.
var numericEdgeIRCases = []struct {
	name string
	main string
	want int
}{
	// #4329 — non-trapping division.
	{"div-zero", `var a=100; var b=0; if (a/b == 0) { return 7; } return 1;`, 7},
	{"mod-zero", `var a=100; var b=0; if (a%b == 100) { return 7; } return 1;`, 7},
	{"i64-div-zero", `var a: i64 = 9000000000; var b: i64 = 0; if (a / b == 0) { return 7; } return 1;`, 7},
	{"i64-mod-zero", `var a: i64 = 9000000000; var b: i64 = 0; if (a % b == a) { return 7; } return 1;`, 7},
	{"intmin-div", `var lo: i32 = -2147483647 - 1; if (lo / -1 < 0) { return 7; } return 1;`, 7},
	{"intmin-mod", `var lo: i32 = -2147483647 - 1; if (lo % -1 == 0) { return 7; } return 1;`, 7},
	{"div-normal", `var a=17; var b=5; if (a/b == 3 && a%b == 2) { return 7; } return 1;`, 7},
	// #4330 — i32 shift-count masking (mod 32).
	{"shl-hi", `var one=1; if ((one << 40) == 256) { return 7; } return 1;`, 7},
	{"shr-hi", `var x=256; if ((x >> 34) == 64) { return 7; } return 1;`, 7},
	{"shl-normal", `var x=255; if ((x << 3) == 2040 && (x >> 1) == 127) { return 7; } return 1;`, 7},
	// #4333 — signed-zero preserving negation (unary `-z`, not `0.0 - z`).
	{"neg-zero", `var z: f64 = 0.0; var nz = -z; if (1.0 / nz < 0.0) { return 7; } return 1;`, 7},
	{"neg-normal", `var x: f64 = 3.5; var y = -x; if (y < 0.0 && (0.0 - y) > 3.0) { return 7; } return 1;`, 7},
	// #4332 — saturating f64 -> int.
	{"sat-pos", `var big = 1000000000000.0; if ((big as i32) == 2147483647) { return 7; } return 1;`, 7},
	{"sat-neg", `var big = 1000000000000.0; var imin: i32 = -2147483647 - 1; if (((0.0 - big) as i32) == imin) { return 7; } return 1;`, 7},
	{"sat-nan", `var nan = 0.0 / 0.0; if ((nan as i32) == 0) { return 7; } return 1;`, 7},
	{"sat-inrange", `if ((42.7 as i32) == 42) { return 7; } return 1;`, 7},
	{"sat64-huge", `var h = 1000000000000000000000.0; var r: i64 = h as i64; if (r == 9223372036854775807) { return 7; } return 1;`, 7},
	{"sat64-nan", `var nan = 0.0 / 0.0; var r: i64 = nan as i64; if (r == 0) { return 7; } return 1;`, 7},
}

func numericEdgeIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostNumericEdgesIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path, and runs the emitted binary.
func TestSelfHostNumericEdgesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range numericEdgeIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(numericEdgeIRSrc(tc.main))
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

// TestSelfHostNumericEdgesIRWasm runs the same cases through the wasm IR
// backend (which additionally exercises the i32.trunc_sat_f64_s + f64.neg
// lowerings). The x86 idivq/shift/cvttsd2si faults don't exist on wasm, but
// the saturating-cast and signed-zero cases are the shared regressions.
func TestSelfHostNumericEdgesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host numeric-edge wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range numericEdgeIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(numericEdgeIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "numedge_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("numeric-edge wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
