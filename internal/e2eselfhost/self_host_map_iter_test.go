package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapIterCases exercise the self-host Map iteration API: m.iter() →
// MapIter[K,V], then has_next() / key() / value() / advance() over the
// parallel keys[]/values[] arrays. Needed by std/json's json_encode
// (which walks a JObject's Map). Exit codes cross-checked vs the Go
// backend.
var mapIterCases = []struct {
	name string
	src  string
	exit int
}{
	{"key-len-plus-value", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 10); m = m.insert(\"bb\", 20); var it: MapIter[string,i32] = m.iter(); var sum: i32 = 0; while (it.has_next()) { sum = sum + it.key().len() + it.value(); it.advance(); } return sum; }", 33},
	{"empty", "function main(): i32 { var m: Map[string,i32] = map_new(4); var it: MapIter[string,i32] = m.iter(); var n: i32 = 0; while (it.has_next()) { n = n + 1; it.advance(); } return n + 50; }", 50},
	{"string-values", "function main(): i32 { var m: Map[string,string] = map_new(4); m = m.insert(\"k1\", \"abc\"); m = m.insert(\"k2\", \"de\"); var it: MapIter[string,string] = m.iter(); var t: i32 = 0; while (it.has_next()) { t = t + it.value().len(); it.advance(); } return t; }", 5},
	{"count", "function main(): i32 { var m: Map[string,i32] = map_new(8); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); var it: MapIter[string,i32] = m.iter(); var c: i32 = 0; while (it.has_next()) { c = c + 1; it.advance(); } return c; }", 3},
}

// TestSelfHostMapIterX86_64 compiles Map-iteration programs with the
// self-hosted x86-64 compiler and checks exit codes.
func TestSelfHostMapIterX86_64(t *testing.T) {
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

	for _, tc := range mapIterCases {
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

// TestSelfHostMapIterArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapIterArm64(t *testing.T) {
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

	for _, tc := range mapIterCases {
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
