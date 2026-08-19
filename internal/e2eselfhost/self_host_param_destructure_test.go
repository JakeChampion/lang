package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// paramDestructureCases pin tuple-destructuring parameters
// `function f((a, b): (T, U))` on the self-host compiler. The parser
// desugars the pattern into a synthetic `__ptuple_<line>_<col>` param
// plus a leading `var (a, b) = <synth>;` destructure (mirroring the
// native parser), so these ride the same proven destructure lowering
// the tupleDestructureIRCases pin — each case carries the same fresh
// struct temp whose IR-only reclaim free (`call __fn___fern_arr_dec`)
// proves the module took the IR path, and `t.x - t.y` pads 0 into the
// exit code. Covers a named function, a second-position param (mixed
// param list), a verbose lambda, and an arrow lambda.
var paramDestructureCases = []struct {
	name string
	src  string
	exit int
}{
	{"fn-param", "struct Point { x: i32, y: i32 } function add((a, b): (i32, i32)): i32 { return a + b; } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return add((30, 12)) + pad; }", 42},
	{"second-position", "struct Point { x: i32, y: i32 } function scale(k: i32, (lo, hi): (i32, i32)): i32 { return k * (hi - lo); } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; return scale(21, (3, 5)) + pad; }", 42},
	{"lambda", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var f = function((x, y): (i32, i32)): i32 { return x * y; }; return f((6, 7)) + pad; }", 42},
	{"arrow-lambda", "struct Point { x: i32, y: i32 } function main(): i32 { var t: Point = Point { x: 1, y: 1 }; var pad: i32 = t.x - t.y; var g = ((lo, hi): (i32, i32)) => hi - lo; return g((5, 47)) + pad; }", 42},
}

// TestSelfHostParamDestructureX86_64 compiles each case through the
// self-hosted x86-64 driver (asm_run, IR default-on), asserts the IR
// path was taken (the reclaimed-temp struct free), and asserts the
// destructured exit code.
func TestSelfHostParamDestructureX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range paramDestructureCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			if bytes.Count(asm, []byte("call __fn___fern_arr_dec")) == 0 {
				t.Fatalf("%s: no struct free emitted — the param-destructure IR path was NOT exercised", tc.name)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
