package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The Go compiler's twin of TestSelfHostHTTPHandlerCensus, and the
// bounded-serve exit criterion of #9853 on this compiler: std/serve's
// production accept loop bounded to 32 completed requests, every response
// checked, and the leak census balanced with zero live bytes.
func TestHTTPHandlerCensus(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			checkHTTPHandlerCensus(t, compiler, target, nativeServerRunner(t, target), e2eharness.RunHTTPHandlerCensus, 32)
		})
	}
}

func TestArm64DarwinHTTPHandlerCensus(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkHTTPHandlerCensus(t, buildFernCLI(t), "arm64-darwin", func(p string) *exec.Cmd { return exec.Command(p) }, e2eharness.RunHTTPHandlerCensus, 32)
}

// nativeServerRunner is how a built server is launched for a target: under
// qemu where the host cannot run it, directly otherwise.
func nativeServerRunner(t *testing.T, target string) func(string) *exec.Cmd {
	t.Helper()
	if target == "arm64-linux" {
		q := arm64QemuOrEmpty(t)
		return func(p string) *exec.Cmd { return runArm64Bin(q, p) }
	}
	q := x86QemuOrEmpty(t)
	return func(p string) *exec.Cmd {
		if q != "" {
			return exec.Command(q, p)
		}
		return exec.Command(p)
	}
}

// checkHTTPHandlerCensus builds the bounded production loop for a target
// under the leak check, drives it with `client` for `rounds` requests, and
// requires the census balanced with zero live bytes.
func checkHTTPHandlerCensus(t *testing.T, compiler, target string, run func(string) *exec.Cmd, client func(*testing.T, *exec.Cmd, int) string, rounds int) {
	t.Helper()
	dir := t.TempDir()
	src, bin := filepath.Join(dir, "server.fern"), filepath.Join(dir, "server")
	if err := os.WriteFile(src, []byte(e2eharness.HTTPHandlerCensusSource(t, "../../..", rounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-target", target, "-o", bin, src}
	cmd := exec.Command(compiler, args...)
	cmd.Env = append(os.Environ(), "FERN_LEAKCHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out := client(t, run(bin), rounds)
	allocs, frees, live := leakSummaryIn(t, out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatal("bounded HTTP handler leaked")
	}
}

// leakSummaryIn finds the census report among a server's other output.
func leakSummaryIn(t *testing.T, out string) (allocs, frees, live int64) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			return parseLeakCheckLine(t, line+"\n")
		}
	}
	t.Fatalf("no leakcheck summary in the server's output:\n%s", out)
	return 0, 0, 0
}
