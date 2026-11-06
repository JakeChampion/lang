package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmHTTPHandlerCensus(t *testing.T) {
	checkSelfHostWasmHTTPHandlerCensus(t, e2eharness.RunWasiHTTPHandlerCensus, 32)
}

func TestSelfHostWasmHTTPKeepAlive(t *testing.T) {
	checkSelfHostWasmHTTPHandlerCensus(t, e2eharness.RunWasiHTTPKeepAlive, e2eharness.KeepAliveCycle)
}

func checkSelfHostWasmHTTPHandlerCensus(t *testing.T, client func(*testing.T, string, int) string, rounds int) {
	t.Helper()
	e2eharness.Wasmtime(t)
	dir := t.TempDir()
	driver := selfHostCLIForHost(t).cli
	src, wat := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wat")
	if err := os.WriteFile(src, []byte(e2eharness.WasiHTTPHandlerCensusSource(t, "../../..", rounds)), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(driver, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR_REPORT=1", "FERN_LEAKCHECK=1")
	report, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	requireCompleteHTTPSemanticLowering(t, report)
	out := client(t, e2eharness.AdaptPreview1Component(t, wat), rounds)
	allocs, frees, live := leakSummaryOf(t, "WASI HTTP handler", out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs != frees || live != 0 {
		t.Fatal("bounded WASI HTTP handler leaked")
	}
}
