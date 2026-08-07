package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// `cli_color_enabled()` is the gate every coloriser in `std/cli` consults,
// and it is decided entirely by the environment — so it cannot be pinned by
// a conformance case (the case format has no way to set a variable) and it
// needs the real CLI rather than an in-process check.
//
// Three conventions, in precedence order: FORCE_COLOR forces colour on,
// NO_COLOR forces it off, TERM=dumb turns it off. FORCE_COLOR wins over
// NO_COLOR because it is the more deliberate signal — NO_COLOR is usually
// exported once in a profile, FORCE_COLOR is set for one run.
//
// Terminal auto-detection is deliberately NOT covered: there is no isatty
// primitive in the runtime, so a tool piped to a file with none of these set
// still emits colour. That gap is tracked separately; this test pins what
// the environment can express today.
func TestCliColorGateHonoursTheConventions(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "gate.fern")
	if err := os.WriteFile(src, []byte(`import "std/cli";
function main(): i32 {
    if (cli.cli_color_enabled()) { return 1; }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cases := []struct {
		name  string
		env   []string
		color bool
	}{
		{"unset", nil, true},
		{"NO_COLOR disables", []string{"NO_COLOR=1"}, false},
		{"NO_COLOR empty still disables", []string{"NO_COLOR="}, false},
		{"TERM=dumb disables", []string{"TERM=dumb"}, false},
		{"TERM=xterm allows", []string{"TERM=xterm"}, true},
		{"FORCE_COLOR forces on", []string{"FORCE_COLOR=1"}, true},
		{"FORCE_COLOR beats NO_COLOR", []string{"FORCE_COLOR=1", "NO_COLOR=1"}, true},
		{"FORCE_COLOR beats TERM=dumb", []string{"FORCE_COLOR=1", "TERM=dumb"}, true},
		{"NO_COLOR beats TERM=xterm", []string{"NO_COLOR=1", "TERM=xterm"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, "-interp", src)
			// A clean base: the ambient environment of a CI runner may
			// itself set TERM or NO_COLOR, which would decide the case
			// instead of the row under test.
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, tc.env...)
			var out, errb bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &errb
			_ = cmd.Run()
			got := cmd.ProcessState.ExitCode() == 1
			if got != tc.color {
				t.Fatalf("env %v: colour=%v, want %v\nstderr: %s",
					tc.env, got, tc.color, errb.String())
			}
		})
	}
}
