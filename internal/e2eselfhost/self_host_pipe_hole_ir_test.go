package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The `_` topic placeholder in piped calls (`x |> f(a, _)` → `f(a, x)`)
// desugars in the self-host parser's pipe_desugar (mirroring the native
// parsePipe): a direct `_` arg is replaced by the piped LHS instead of
// the LHS being prepended. Pure parse-time rewrite — the self-host IR
// path sees an ordinary ExprCall. Exit codes oracle-check the arithmetic.
var pipeHoleIRCases = []struct {
	name string
	main string
	want int
}{
	// Hole in the second slot: sub(10, 3) = 7.
	{"hole-second", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { var x: i32 = 3; return x |> sub(10, _); }`, 7},
	// Position-distinguishing middle slot: pick(1, 20, 3) = 1 + 200 + 3.
	{"hole-middle", `function pick(a: i32, b: i32, c: i32): i32 { return a + b * 10 + c; }
function main(): i32 { return 20 |> pick(1, _, 3); }`, 204},
	// Nested pipes: inner hole binds inner LHS → sub(20, sub(5, 3)) = 18.
	{"nested", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { var x: i32 = 3; return 20 |> sub(_, x |> sub(5, _)); }`, 18},
	// Chained hole stages: sub(9,4)=5, then sub(8,5)=3.
	{"chained", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(9, _) |> sub(8, _); }`, 3},
	// A plain (prepending) stage after a hole stage still prepends.
	{"hole-then-prepend", `function sub(a: i32, b: i32): i32 { return a - b; }
function main(): i32 { return 4 |> sub(10, _) |> sub(2); }`, 4},
}

// TestSelfHostPipeHoleIRX86_64 routes each case through the self-hosted
// x86-64 IR driver and runs the produced binary, oracle-checking its
// exit code.
func TestSelfHostPipeHoleIRX86_64(t *testing.T) {
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

	for _, tc := range pipeHoleIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
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
