package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// The self-host x86-64 emitter (asm.fern) gains a read_all_stdin
// builtin: read fd 0 to EOF into one string box (looping the read
// syscall), so a self-hosted tool can pull in a whole multi-line
// source — blank lines and all — which read_line cannot (read_line
// can't tell a blank line from EOF). Prerequisite for feeding the
// compiler its own multi-module source.
//
// This compiles `read_all_stdin().len()` through asm.fern (via the
// bundle_run driver, single-module), feeds multi-line input including
// a blank line, and asserts the byte count. Also bumps the emitted
// heap to 256 MiB so a read buffer + a real compile fit.
func TestSelfHostReadAllStdinX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// Single-module program using read_all_stdin, compiled by the
	// self-host emitter.
	prgm := "///MODULE main\nfunction main(): i32 { var s: string = read_all_stdin(); return s.len(); }\n"
	progAsm := runCapture(t, gcc, runner, driverBin, []byte(prgm))
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progAsmPath := filepath.Join(dir, "ras.s")
	progBin := filepath.Join(dir, "ras")
	if err := os.WriteFile(progAsmPath, progAsm, 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", progAsmPath, "-o", progBin).CombinedOutput(); err != nil {
		t.Fatalf("gcc on emitted program: %v\n%s", err, out)
	}

	// Multi-line input with a blank line: "ab\n\ncd\n" = 7 bytes.
	for _, c := range []struct {
		in   string
		want int
	}{
		{"ab\n\ncd\n", 7},
		{"hello", 5},
		{"", 0},
	} {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(c.in))
		_, _ = cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != c.want {
			t.Errorf("read_all_stdin(%q).len() = %d, want %d", c.in, code, c.want)
		}
	}
}
