package e2e

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// #9853's per-request gate, bump half, through the Go compiler on both
// native targets: 100k requests on one keep-alive connection, and the bump
// high-water mark a tenth of the way in is the one at the last request.
// The self-host twin is TestSelfHostBumpPerRequest.
func TestBumpPerRequest(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			rounds := e2eharness.BumpPerRequestRounds
			if target == "arm64-linux" && arm64QemuOrEmpty(t) != "" {
				// Under qemu one request costs about a millisecond; a tenth
				// of the count still warms the free lists ten times over.
				rounds /= 10
			}
			port := freeLoopbackPort(t)
			bin := buildNativeServer(t, compiler, target, e2eharness.BumpPerRequestServerSource(port))
			e2eharness.StartServerProcess(t, nativeServerRunner(t, target)(bin))
			e2eharness.CheckBumpPerRequest(t, fmt.Sprintf("127.0.0.1:%d", port), rounds)
		})
	}
}

// The Go compiler's twin of TestSelfHostServeListenFailure: a server on a
// port something else holds exits 98 and says why, from the single loop
// and from the supervisor alike.
func TestServeListenFailure(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, supervised := range []bool{false, true} {
		t.Run(fmt.Sprintf("supervised=%v", supervised), func(t *testing.T) {
			held := heldLoopbackPort(t)
			bin := buildNativeServer(t, compiler, "x86-64-linux", e2eharness.ListenFailureServerSource(held, supervised))
			e2eharness.CheckListenFailure(t, nativeServerRunner(t, "x86-64-linux")(bin), held)
		})
	}
}

// heldLoopbackPort answers a port a listener of the test's holds until the
// test ends, on every interface, as a server's own listen would bind it.
func heldLoopbackPort(t *testing.T) int {
	t.Helper()
	held, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	return held.Addr().(*net.TCPAddr).Port
}

// buildNativeServer compiles src with the Go compiler for target.
func buildNativeServer(t *testing.T, compiler, target, src string) string {
	t.Helper()
	dir := t.TempDir()
	path, bin := filepath.Join(dir, "server.fern"), filepath.Join(dir, "server")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(compiler, "-target", target, "-o", bin, path).CombinedOutput(); err != nil {
		t.Fatalf("build for %s: %v\n%s", target, err, out)
	}
	return bin
}
