package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapLiteralCases cover the `Map { k0: v0, k1: v1, … }` literal, which
// the parser desugars to a chained `map_new[_i32](n).set(k0,v0)…`
// (map_new_i32 when the first key is a number literal, so the chained
// .set dispatch picks integer key comparison). Exit codes cross-checked
// vs the Go backend.
var mapLiteralCases = []struct {
	name string
	src  string
	exit int
}{
	{"i32-keys", "function main(): i32 { var m: Map[i32,i32] = Map { 1: 10, 2: 20, 3: 12 }; match (m.get(2)) { Some(v) => { return v; }, None => { return 0; } } }", 20},
	{"string-keys", "function main(): i32 { var m: Map[string,i32] = Map { \"a\": 40, \"b\": 2 }; var t: i32 = 0; match (m.get(\"a\")) { Some(x) => { t = t + x; }, None => {} } match (m.get(\"b\")) { Some(x) => { t = t + x; }, None => {} } return t; }", 42},
	{"len-has", "function main(): i32 { var m: Map[i32,i32] = Map { 7: 1, 8: 2 }; if (m.len() == 2 && m.has(8) && !m.has(9)) { return 9; } return 0; }", 9},
	{"empty", "function main(): i32 { var m: Map[i32,i32] = Map { }; m.set(5, 42); match (m.get(5)) { Some(v) => { return v; }, None => { return 0; } } }", 42},
}

// TestSelfHostMapLiteralX86_64 — `Map { … }` literals with the
// self-hosted x86-64 compiler.
func TestSelfHostMapLiteralX86_64(t *testing.T) {
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

	for _, tc := range mapLiteralCases {
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

// TestSelfHostMapLiteralArm64 — CI-gated arm64 counterpart.
func TestSelfHostMapLiteralArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "asm_arm64.fern", "asm_arm64_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_arm64_run.fern", "driver")

	for _, tc := range mapLiteralCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src))
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
