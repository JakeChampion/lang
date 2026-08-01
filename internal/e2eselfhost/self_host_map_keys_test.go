package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapKeysValuesCases cover `m.keys()` / `m.values()` — the map box
// already holds the parallel keys[]/values[] arrays at offset 0/8, so
// these return them directly. The result is an array of the key/value
// type (array_i32 for i32 keys/values, array_string for strings), so
// array methods (.sum()/.len()) chain off it. Exit codes cross-checked
// vs the Go backend.
var mapKeysValuesCases = []struct {
	name string
	src  string
	exit int
}{
	{"keys-sum-literal", "function main(): i32 { var m: Map[i32,i32] = Map { 10: 1, 20: 2, 12: 3 }; return m.keys().sum(); }", 42},
	{"keys-sum-built", "function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(7, 0); m = m.insert(35, 0); return m.keys().sum(); }", 42},
	{"values-sum", "function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(1, 10); m = m.insert(2, 20); return m.values().sum(); }", 30},
	{"keys-len-string", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"ab\", 1); m = m.insert(\"c\", 2); return m.keys().len() + 40; }", 42},
}

// TestSelfHostMapKeysX86_64 — m.keys()/m.values() with the self-hosted
// x86-64 compiler.
func TestSelfHostMapKeysX86_64(t *testing.T) {
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

	for _, tc := range mapKeysValuesCases {
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

// TestSelfHostMapKeysArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapKeysArm64(t *testing.T) {
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

	for _, tc := range mapKeysValuesCases {
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
