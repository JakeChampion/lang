// CI-DARK: FERN_ARM64_VERDICT_CHILD — The test uses this flag to identify its child process.
package e2eharness

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestArm64ToolingMissingVerdict(t *testing.T) {
	switch os.Getenv("FERN_ARM64_VERDICT_CHILD") {
	case "1":
		Arm64Tooling(t)
		return
	case "runner":
		Arm64Runner(t)
		return
	case "assembler":
		Arm64Assembler(t)
		return
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" {
		t.Skip("a native arm64 host runs arm64 binaries with no tooling, so there is no missing-toolchain verdict to observe")
	}
	for _, tc := range []struct {
		name     string
		child    string
		require  string
		wantFail bool
		wantText string
	}{
		{name: "skips by default", child: "1", require: "", wantFail: false, wantText: "not available"},
		{name: "fails when required", child: "1", require: "1", wantFail: true, wantText: "covers nothing"},
		{name: "runner skips by default", child: "runner", require: "", wantFail: false, wantText: "no qemu-aarch64"},
		{name: "runner fails when required", child: "runner", require: "1", wantFail: true, wantText: "covers nothing"},
		{name: "assembler goes on by default", child: "assembler", require: "", wantFail: false, wantText: "assembling nothing"},
		{name: "assembler fails when required", child: "assembler", require: "1", wantFail: true, wantText: "covers nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestArm64ToolingMissingVerdict$", "-test.v")
			cmd.Env = append(os.Environ(),
				"FERN_ARM64_VERDICT_CHILD="+tc.child,
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
