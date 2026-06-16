package e2e

import (
	"bytes"
	"os/exec"
	"testing"
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
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// Single-module program using read_all_stdin, compiled by the
	// self-host emitter (file-based driver — no imports, so its source
	// is the whole program).
	prgm := "function main(): i32 { var s: string = read_all_stdin(); return s.len(); }\n"
	progAsm, progDir := compileSourceModload(t, runner, driverBin, prgm)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	progBin := buildBin(t, gcc, progDir, "ras", progAsm)

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
