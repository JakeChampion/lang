package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const walkieApp = "examples/android/walkie"

// runWalkieTest builds one test program of the walkie-talkie app for the host
// and requires that its TAP report has no failure.
func runWalkieTest(t *testing.T, name string) {
	t.Helper()
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skipf("no self-host target runs natively on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	bin := filepath.Join(t.TempDir(), strings.TrimSuffix(name, ".fern"))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), "-target", host, "-o", bin,
		e2eharness.RepoPath(walkieApp, name), e2eharness.SelfHostStdlibRoot(t))
	cmd.Env = e2eharness.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	out, err := exec.Command(bin).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "# fail 0") {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

// The session engine: the state machine, the floor control and the
// protocol, with two engines on a simulated link (acceptance test C).
func TestWalkieSessionEngine(t *testing.T) {
	runWalkieTest(t, "session_test.fern")
}
