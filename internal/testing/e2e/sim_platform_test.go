package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// `tests/stdlib/sim_platform_test.fern` runs a handler's platform over
// the simulation (std/sim_platform): the clock, randomness, configuration
// and the public-only HTTP route answered from a `Sim`, a scripted network
// and what the test set, with the shapes the host and the mock answer. It
// is the platform's sim parity suite (#9855), so it runs on every backend,
// and on the self-host compiler in internal/testing/e2ecompiler's std/test e2e.

const simPlatformSuite = "tests/stdlib/sim_platform_test.fern"

var simPlatformWant = []string{"# Suite: std/sim_platform", "# pass 13", "# fail 0", "1..13"}

// hostProxyEnv is a proxy configuration for the machine that leads nowhere.
// Every leg runs under it: the simulated network's requests are virtual, so
// the host's proxies must not reach them.
var hostProxyEnv = []string{"http_proxy=http://192.0.2.1:9", "https_proxy=http://192.0.2.1:9", "HTTPS_PROXY=http://192.0.2.1:9"}

// runUnderHostProxy runs cmd with hostProxyEnv added to the test's
// environment, returning its combined output and exit code.
func runUnderHostProxy(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	cmd.Env = append(os.Environ(), hostProxyEnv...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("%s did not exit normally\n%s", cmd.Path, out.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func checkSimPlatformOutput(t *testing.T, leg, out string) {
	t.Helper()
	for _, w := range simPlatformWant {
		if !strings.Contains(out, w) {
			t.Errorf("%s: output missing %q\nfull output:\n%s", leg, w, out)
		}
	}
}

func TestRunnerSimPlatformExamplePasses(t *testing.T) {
	bin := buildLangBinForInterp(t)
	out, code := runUnderHostProxy(t, exec.Command(bin, "-interp", langSrcAbs(t, simPlatformSuite)))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "interp", out)
}

func simPlatformSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(langSrcAbs(t, simPlatformSuite))
	if err != nil {
		t.Fatalf("read %s: %v", simPlatformSuite, err)
	}
	return string(src)
}

func TestSimPlatformNativeX86_64(t *testing.T) {
	bin, runner := e2eharness.CompileX86_64Bin(t, simPlatformSource(t))
	out, code := runUnderHostProxy(t, e2eharness.RunX86_64Bin(runner, bin))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "x86-64", out)
}

func TestSimPlatformArm64(t *testing.T) {
	bin, qemu := e2eharness.CompileArm64Bin(t, simPlatformSource(t))
	out, code := runUnderHostProxy(t, e2eharness.RunArm64Bin(qemu, bin))
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "arm64", out)
}

func TestWASMSimPlatform(t *testing.T) {
	out, _, code := runWasmArtifact(t, buildResultComponent(t, simPlatformSource(t)), runOpts{envs: hostProxyEnv})
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	checkSimPlatformOutput(t, "wasm", out)
}
