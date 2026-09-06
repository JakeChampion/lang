package e2eharness

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// FERN_REQUIRE_X86_64_TOOLING turns X86_64Tooling's skip into a failure, the
// mirror of FERN_REQUIRE_ARM64_TOOLING and for the same reason: the x86-64
// native-link parity gates run for real on one lane only, so a silent skip
// there reports green while covering nothing.
//
// The flag became load-bearing when the bare-`gcc` fallback was gated to
// x86-64 hosts. That gate is right — an aarch64 `gcc` cannot assemble what the
// x86-64 backend emits — but it also means the x86_64 leg losing its compiler
// now reads as a skip rather than as the assembler error it used to be.
//
// Both directions are checked, because the value of the flag is entirely in
// the difference between them. X86_64Tooling ends the test it is given
// (Skipf / Fatalf), so the two verdicts are observed in a subprocess, and PATH
// is emptied rather than the binaries moved, since discovery finds them with
// exec.LookPath.
//
// CI-DARK: FERN_X86_64_VERDICT_CHILD — this names the re-exec'd child of this
// test, not a lane's coverage knob. The parent sets it when it spawns the
// child, so a workflow setting it would only make the child run as the parent.
func TestX86_64ToolingMissingVerdict(t *testing.T) {
	if os.Getenv("FERN_X86_64_VERDICT_CHILD") == "1" {
		X86_64Tooling(t)
		return
	}
	for _, tc := range []struct {
		name     string
		require  string
		wantFail bool
		wantText string
	}{
		{name: "skips by default", require: "", wantFail: false, wantText: "on PATH"},
		{name: "fails when required", require: "1", wantFail: true, wantText: "covers nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestX86_64ToolingMissingVerdict$", "-test.v")
			cmd.Env = append(os.Environ(),
				"FERN_X86_64_VERDICT_CHILD=1",
				"PATH="+t.TempDir(),
				"FERN_REQUIRE_X86_64_TOOLING="+tc.require,
			)
			out, err := cmd.CombinedOutput()
			if gotFail := err != nil; gotFail != tc.wantFail {
				t.Fatalf("child failed=%v, want %v\n%s", gotFail, tc.wantFail, out)
			}
			if !strings.Contains(string(out), tc.wantText) {
				t.Fatalf("child output missing %q:\n%s", tc.wantText, out)
			}
		})
	}
}
