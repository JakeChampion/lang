package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWasmHTTPHandlerCensus(t *testing.T) {
	component := buildLeakCheckCLIComponent(t, e2eharness.WasiHTTPHandlerCensusSource(t, "../../..", 32), false)
	out := e2eharness.RunWasiHTTPHandlerCensus(t, component, 32)
	allocs, frees, live := leakSummaryIn(t, out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatal("bounded WASI HTTP handler leaked")
	}
}
