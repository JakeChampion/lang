package e2ecompiler

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
		name    string
		src     string
		mode    []string
		sameOut bool
	}{
		// The exit code is the program's result, so it carries the answer
		// rather than just "it ran".
		{"interp-explicit-dash", "function main(): i32 { return 7; }\n", []string{"-interp", "-"}, false},
		// No path at all is the same thing spelled shorter, and is what a
		// pipeline stage writes.
		{"interp-no-path", "function main(): i32 { return 12; }\n", []string{"-interp"}, false},
		// A source with no trailing newline must not lose its last line.
		{"interp-no-trailing-newline", "function main(): i32 { return 3; }", []string{"-interp"}, false},
		// Multi-line, so the line-by-line read has to reassemble the source
		// verbatim — a dropped or doubled newline changes the program.
		{"interp-multiline", "function twice(n: i32): i32 {\n    return n + n;\n}\n\nfunction main(): i32 {\n    return twice(11);\n}\n", []string{"-interp"}, false},
		{"check-explicit-dash", "function main(): i32 { return 0; }\n", []string{"-check", "-"}, false},
		{"check-no-path", "function main(): i32 { return 0; }\n", []string{"-check"}, false},
		// An ill-typed program on stdin must be refused by both, or stdin is a
		// hole in the check rather than a way into it.
		{"check-ill-typed", "function main(): i32 { return \"nope\"; }\n", []string{"-check"}, false},
		// `-fmt -` prints stdin formatted, byte for byte the same under both.
		{name: "fmt-dash-reads-stdin", src: "function main(): i32 {return 1;}\n", mode: []string{"-fmt", "-"}, sameOut: true},
		// There is no file to write back to.
		{name: "fmt-dash-write-refused", src: "function main(): i32 { return 1; }\n", mode: []string{"-fmt", "-w", "-"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			nativeOut, nativeCode := run(nativeBin, c.src, c.mode...)
			shOut, shCode := run(driverBin, c.src, c.mode...)
			if shCode != nativeCode {
				t.Errorf("exit code differs: native %d, self-host %d\n--- self-host ---\n%s",
					nativeCode, shCode, shOut)
			}
			if c.sameOut && shOut != nativeOut {
				t.Errorf("output differs\n--- native ---\n%s\n--- self-host ---\n%s", nativeOut, shOut)
			}
		})
	}
}
