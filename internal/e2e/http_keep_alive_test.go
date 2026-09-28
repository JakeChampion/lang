package e2e

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// Persistent connections on the production accept loop (#9854): the same
// bounded loop as the census, driven by HTTPKeepAliveRequests, whose
// requests are pipelined pairs on one connection, an HTTP/1.0 request
// asking to keep it, the fourth request reaching the per-connection cap and
// answered with `Connection: close`, and HTTP/1.0 and `Connection: close`
// requests each ending theirs. Every response's `Connection` is checked,
// every close the server owes is read as EOF, and the census must balance.
func TestHTTPKeepAlive(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, tc := range []struct{ target, backend string }{
		{"x86-64-linux", ""}, {"x86-64-linux", "ssa"}, {"arm64-linux", ""}, {"arm64-linux", "ssa"},
	} {
		t.Run(tc.target+"/"+tc.backend, func(t *testing.T) {
			checkHTTPHandlerCensus(t, compiler, tc.target, tc.backend, nativeServerRunner(t, tc.target), e2eharness.RunHTTPKeepAlive, e2eharness.KeepAliveCycle)
		})
	}
}

func TestArm64DarwinHTTPKeepAlive(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkHTTPHandlerCensus(t, buildFernCLI(t), "arm64-darwin", "", func(p string) *exec.Cmd { return exec.Command(p) }, e2eharness.RunHTTPKeepAlive, e2eharness.KeepAliveCycle)
}

// TestWasmHTTPKeepAlive is the same loop against real wasi:sockets.
func TestWasmHTTPKeepAlive(t *testing.T) {
	component := buildLeakCheckComponentPrinting(t, e2eharness.WasiHTTPHandlerCensusSource(t, "../..", e2eharness.KeepAliveCycle), false, false)
	out := e2eharness.RunWasiHTTPKeepAlive(t, component, e2eharness.KeepAliveCycle)
	allocs, frees, live := leakSummaryIn(t, out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatal("bounded WASI keep-alive loop leaked")
	}
}
