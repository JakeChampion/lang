package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

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
		{"interp-explicit-dash", "function main(): i32 { return 7; }\n", []string{"-interp", "-"}, false},
		{"interp-no-path", "function main(): i32 { return 12; }\n", []string{"-interp"}, false},
		{"interp-no-trailing-newline", "function main(): i32 { return 3; }", []string{"-interp"}, false},
		{"interp-multiline", "function twice(n: i32): i32 {\n    return n + n;\n}\n\nfunction main(): i32 {\n    return twice(11);\n}\n", []string{"-interp"}, false},
		{"check-explicit-dash", "function main(): i32 { return 0; }\n", []string{"-check", "-"}, false},
		{"check-no-path", "function main(): i32 { return 0; }\n", []string{"-check"}, false},
		{"check-ill-typed", "function main(): i32 { return \"nope\"; }\n", []string{"-check"}, false},
		{name: "fmt-dash-reads-stdin", src: "function main(): i32 {return 1;}\n", mode: []string{"-fmt", "-"}, sameOut: true},
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
