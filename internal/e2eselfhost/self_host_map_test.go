package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapCases exercise the self-host Map runtime (string-keyed, 8-byte
// values): map_new, set (insert + update), get → Option, has, len.
// Values cross-checked vs the Go backend.
var mapCases = []struct {
	name string
	src  string
	exit int
}{
	{"set-get-sum", "function main(): i32 { var m: Map[string,i32] = map_new(8); m = m.insert(\"a\", 10); m = m.insert(\"b\", 32); var r: i32 = 0; match (m.get(\"a\")) { Some(v) => { r = r + v; }, None => { } } match (m.get(\"b\")) { Some(v) => { r = r + v; }, None => { } } return r; }", 42},
	{"update", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 10); m = m.insert(\"a\", 11); match (m.get(\"a\")) { Some(v) => { return v; }, None => { return 0; } } return 0; }", 11},
	{"has-len", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"x\", 7); m = m.insert(\"y\", 8); if (m.has(\"x\") && m.has(\"y\") && !m.has(\"z\")) { return m.len() + 40; } return 0; }", 42},
	{"get-absent", "function main(): i32 { var m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 1); match (m.get(\"absent\")) { Some(v) => { return 1; }, None => { return 99; } } return 0; }", 99},
}

// TestSelfHostMapX86_64 compiles map programs with the self-hosted
// compiler and checks exit codes.
func TestSelfHostMapX86_64(t *testing.T) {
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

	for _, tc := range mapCases {
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

// TestSelfHostMapArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapArm64(t *testing.T) {
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
	for _, tc := range mapCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			progBin := buildBin(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
