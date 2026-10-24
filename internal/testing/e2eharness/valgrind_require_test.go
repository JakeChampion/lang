// CI-DARK: FERN_VALGRIND_VERDICT_CHILD — The test uses this flag to identify its child process.
package e2eharness

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestValgrindMissingVerdict(t *testing.T) {
	if os.Getenv("FERN_VALGRIND_VERDICT_CHILD") == "1" {
		Valgrind(t)
		return
	}
	for _, tc := range []struct {
		name     string
		require  string
		wantFail bool
		wantText string
	}{
		{name: "skips by default", require: "", wantFail: false, wantText: "skipping the memcheck gate"},
		{name: "fails when required", require: "1", wantFail: true, wantText: "covers nothing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run", "^TestValgrindMissingVerdict$", "-test.v")
			cmd.Env = append(os.Environ(),
				"FERN_VALGRIND_VERDICT_CHILD=1",
				"PATH="+t.TempDir(),
				"FERN_REQUIRE_VALGRIND="+tc.require,
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
