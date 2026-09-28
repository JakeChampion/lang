package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// The Go compiler's twin of TestSelfHostWasmHTTPHandlerCensus: the bounded
// accept loop against real wasi:sockets, on a port the guest picks, with
// every response checked and the census balanced.
func TestWasmHTTPHandlerCensus(t *testing.T) {
	component := buildLeakCheckComponentPrinting(t, e2eharness.WasiHTTPHandlerCensusSource(t, "../..", 32), false, false)
	out := e2eharness.RunWasiHTTPHandlerCensus(t, component, 32)
	allocs, frees, live := leakSummaryIn(t, out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatal("bounded WASI HTTP handler leaked")
	}
}
