package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// forKvCases cover `for (k, v) in m { … }` map destructuring iteration.
// The parser encodes the two names as "k,v"; the emitter iterates the
// map's parallel keys[]/values[] arrays by index, binding k = keys[i+1],
// v = values[i+1]. Exit codes cross-checked vs the Go backend.
var forKvCases = []struct {
	name string
	src  string
	exit int
}{
	{"sum-k-plus-v", "function main(): i32 { var m: Map[i32,i32] = Map { 1: 10, 2: 20, 3: 12 }; var total: i32 = 0; for (k, v) in m { total = total + k + v; } return total; }", 48},
	{"sum-values-built", "function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(5, 100); m = m.insert(7, 50); var t: i32 = 0; for (k, v) in m { t = t + v; } return t; }", 150},
	{"count", "function main(): i32 { var m: Map[i32,i32] = Map { 1: 0, 2: 0, 3: 0, 4: 0 }; var c: i32 = 0; for (k, v) in m { c = c + 1; } return c + 38; }", 42},
	{"string-keys", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 40); m = m.insert(\"b\", 2); var t: i32 = 0; for (k, v) in m { t = t + v; } return t; }", 42},
}

// TestSelfHostForKvX86_64 — `for (k,v) in m` with the self-hosted
// x86-64 compiler.
func TestSelfHostForKvX86_64(t *testing.T) {
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

	for _, tc := range forKvCases {
		t.Run(tc.name, func(t *testing.T) {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

// TestSelfHostForKvArm64 — CI-gated arm64 counterpart.
func TestSelfHostForKvArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range forKvCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
