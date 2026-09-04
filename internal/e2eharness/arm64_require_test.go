package e2eharness

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// FERN_REQUIRE_ARM64_TOOLING turns Arm64Tooling's skip into a failure, for a
// lane that installs the toolchain and is the only place a test runs. Both
// directions are checked, because the value of the flag is entirely in the
// difference between them: a version that always skipped would look identical
// on a machine that has the toolchain.
//
// Arm64Tooling ends the test it is given (Skipf / Fatalf), so the two verdicts
// are observed in a subprocess — the standard shape for testing a helper that
// terminates its caller. PATH is emptied rather than the binaries moved, since
// LookupArm64Tooling finds them with exec.LookPath.
//
// CI-DARK: FERN_ARM64_VERDICT_CHILD — this names the re-exec'd child of this
// test, not a lane's coverage knob. The parent sets it when it spawns the
// child, so a workflow setting it would only make the child run as the parent.
func TestArm64ToolingMissingVerdict(t *testing.T) {
	if os.Getenv("FERN_ARM64_VERDICT_CHILD") == "1" {
		Arm64Tooling(t)
		return
	}
	for _, tc := range []struct {
		name     string
		require  string
		wantFail bool
		wantText string
	}{
		{name: "skips by default", require: "", wantFail: false, wantText: "not available"},
		{name: "fails when required", require: "1", wantFail: true, wantText: "covers nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestArm64ToolingMissingVerdict$", "-test.v")
			cmd.Env = append(os.Environ(),
				"FERN_ARM64_VERDICT_CHILD=1",
				"PATH="+t.TempDir(),
				"FERN_REQUIRE_ARM64_TOOLING="+tc.require,
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
