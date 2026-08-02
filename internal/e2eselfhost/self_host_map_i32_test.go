package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapI32Cases exercise i32-keyed maps (Map[i32, V]). The Map runtime
// compares keys with __fern_str_eq for string keys; for i32 keys the
// dispatch passes a key-kind flag and the runtime takes an integer
// (`==`) compare path instead. Covers set/update/has/len and the
// Option-returning get. Exit codes cross-checked vs the Go backend.
var mapI32Cases = []struct {
	name string
	src  string
	exit int
}{
	{"set-get-update", "function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(7, 40); m = m.insert(11, 99); m = m.insert(7, 42); if (m.len() != 2) { return 1; } if (!m.has(11)) { return 2; } match (m.get(7)) { Some(v) => { return v; }, None => { return 3; } } }", 42},
	{"absent-get", "function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(100, 5); m = m.insert(200, 7); match (m.get(999)) { Some(v) => { return v; }, None => { return 42; } } }", 42},
	{"has-absent", "function main(): i32 { var m: Map[i32,i32] = map_new(4); m = m.insert(1, 1); if (m.has(1) && !m.has(2)) { return 7; } return 0; }", 7},
	{"i32-to-string-val", "function main(): i32 { var m: Map[i32, string] = map_new(4); m = m.insert(1, \"hello\"); match (m.get(1)) { Some(s) => { return s.len(); }, None => { return 0; } } }", 5},
}

// TestSelfHostMapI32X86_64 — i32-keyed maps with the self-hosted
// x86-64 compiler.
func TestSelfHostMapI32X86_64(t *testing.T) {
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

	for _, tc := range mapI32Cases {
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

// TestSelfHostMapI32Arm64 — CI-gated arm64 counterpart.
func TestSelfHostMapI32Arm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapI32Cases {
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
