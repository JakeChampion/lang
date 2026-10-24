package e2e

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestHTTPKeepAlive(t *testing.T) {
	compiler := buildFernCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			checkHTTPHandlerCensus(t, compiler, target, nativeServerRunner(t, target), e2eharness.RunHTTPKeepAlive, e2eharness.KeepAliveCycle)
		})
	}
}

func TestArm64DarwinHTTPKeepAlive(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires native Apple Silicon")
	}
	checkHTTPHandlerCensus(t, buildFernCLI(t), "arm64-darwin", func(p string) *exec.Cmd { return exec.Command(p) }, e2eharness.RunHTTPKeepAlive, e2eharness.KeepAliveCycle)
}

func TestWasmHTTPKeepAlive(t *testing.T) {
	component := buildLeakCheckCLIComponent(t, e2eharness.WasiHTTPHandlerCensusSource(t, "../../..", e2eharness.KeepAliveCycle), false)
	out := e2eharness.RunWasiHTTPKeepAlive(t, component, e2eharness.KeepAliveCycle)
	allocs, frees, live := leakSummaryIn(t, out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Fatal("bounded WASI keep-alive loop leaked")
	}
}
