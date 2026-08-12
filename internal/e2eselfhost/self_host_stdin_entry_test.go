package e2eselfhost

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostStdinEntryDifferentialX86_64 pins `-` (and the bare no-path
// form) against native: a program arriving on stdin must check and evaluate
// exactly as the same program in a file does.
//
// This is the filesystem-free entry point precondition 4 of backend
// retirement needs (#6643) — a browser host has no file to open — and it is a
// plain parity gap besides: native has read `fern -check -` and `fern -interp
// -` since the CLI existed, and the self-host driver refused both.
func TestSelfHostStdinEntryDifferentialX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("stdin entry differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	nativeBin := buildFernCLIBin(t)

	run := func(bin string, stdin string, args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Stdin = strings.NewReader(stdin)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		_ = cmd.Run()
		return out.String(), cmd.ProcessState.ExitCode()
	}

	for _, c := range []struct {
		name string
		src  string
		mode []string
	}{
		// The exit code is the program's result, so it carries the answer
		// rather than just "it ran".
		{"interp-explicit-dash", "function main(): i32 { return 7; }\n", []string{"-interp", "-"}},
		// No path at all is the same thing spelled shorter, and is what a
		// pipeline stage writes.
		{"interp-no-path", "function main(): i32 { return 12; }\n", []string{"-interp"}},
		// A source with no trailing newline must not lose its last line.
		{"interp-no-trailing-newline", "function main(): i32 { return 3; }", []string{"-interp"}},
		// Multi-line, so the line-by-line read has to reassemble the source
		// verbatim — a dropped or doubled newline changes the program.
		{"interp-multiline", "function twice(n: i32): i32 {\n    return n + n;\n}\n\nfunction main(): i32 {\n    return twice(11);\n}\n", []string{"-interp"}},
		{"check-explicit-dash", "function main(): i32 { return 0; }\n", []string{"-check", "-"}},
		{"check-no-path", "function main(): i32 { return 0; }\n", []string{"-check"}},
		// An ill-typed program on stdin must be refused by both, or stdin is a
		// hole in the check rather than a way into it.
		{"check-ill-typed", "function main(): i32 { return \"nope\"; }\n", []string{"-check"}},
		// `-` is stdin for exactly the two modes native reads it in. `-fmt -`
		// is a file error under native, and the self-host accepting it would
		// be a divergence in the permissive direction — the one that lets a
		// program work under one compiler only.
		{"fmt-dash-is-a-file-error", "function main(): i32 { return 1; }\n", []string{"-fmt", "-"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, nativeCode := run(nativeBin, c.src, c.mode...)
			shOut, shCode := run(driverBin, c.src, c.mode...)
			if shCode != nativeCode {
				t.Errorf("exit code differs: native %d, self-host %d\n--- self-host ---\n%s",
					nativeCode, shCode, shOut)
			}
		})
	}
}
