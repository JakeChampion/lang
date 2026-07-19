package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapW64IterIRCases cover map ITERATION over 64-bit value columns (#5253):
// both `for (k, v) in m` and the single-var `for v in m.values()` form bound
// the value with a 32-bit element read (op_arr_get(32)) and no width/sign/f64
// mark on the binding — so an i64/u64/f64-valued map iterated to truncated
// (and, for u64, signed) garbage ON the IR path (a silent wrong answer, not
// an AST fallback: measured 113/199/146/255 against interp oracles 62/7/18/5
// on a pre-fix driver). The fix reads the full 8-byte element (arr_get_i64
// for i64/u64; arr_get width 64 — f64.load — for f64) and marks the binding
// i64/u64/f64, so body uses route through the 64-bit/float ops with the right
// sign. String/i32 columns keep the 4-byte/pointer path — pinned by the
// regression cases.
//
// Routing-pinned to "ir", oracle-checked against the interpreter.
var mapW64IterIRCases = []struct {
	name string
	main string
}{
	// u64 2-var iteration, shift in the body: 62 (was 113).
	{"forin-u64-shr", `import "core/map";
function main(): i32 { var m: Map[i32, u64] = Map { 1: 18000000000000000000 as u64 }; var acc: i32 = 0; for (k, v) in m { acc = acc + ((v >> 58) as i32); } return acc; }`},
	// i64 2-var iteration, wide values summed: 18 (was 146).
	{"forin-i64-sum", `import "core/map";
function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007, 2: 6000000011 }; var acc: i64 = 0; for (k, v) in m { acc = acc + (v % 1000); } return acc as i32; }`},
	// i64 single-var values() iteration: 7 (was 199).
	{"forin-values-i64", `import "core/map";
function main(): i32 { var m: Map[i32, i64] = Map { 1: 5000000007 }; var acc: i32 = 0; for v in m.values() { acc = acc + ((v % 1000) as i32); } return acc; }`},
	// f64 2-var iteration: 5 (was 255).
	{"forin-f64", `import "core/map";
function main(): i32 { var m: Map[i32, f64] = Map { 1: 2.5 }; var acc: f64 = 0.0; for (k, v) in m { acc = acc + v; } return (acc * 2.0) as i32; }`},
	// String-valued 2-var regression (pointer column path unchanged): 10.
	{"forin-str-regress", `import "core/map";
function main(): i32 { var m: Map[i32, string] = Map { 1: "hello", 2: "xy" }; var acc: i32 = 0; for (k, v) in m { acc = acc + v.len() + k; } return acc; }`},
	// i32-valued 2-var + single-var keys() regression (snapshot column): 36.
	{"forin-i32-regress", `import "core/map";
function main(): i32 { var m: Map[i32, i32] = Map { 1: 10, 2: 20 }; var acc: i32 = 0; for (k, v) in m { acc = acc + v + k; } for k2 in m.keys() { acc = acc + k2; } return acc; }`},
}

func TestSelfHostMapW64IterIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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
	for _, tc := range mapW64IterIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
