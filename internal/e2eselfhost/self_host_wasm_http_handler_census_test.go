package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostWasmHTTPHandlerCensus(t *testing.T) {
	for _, tool := range []string{"wasm-tools", "wasmtime"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not on PATH")
		}
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	host := "x86-64-linux"
	if runtime.GOARCH == "arm64" {
		host = "arm64-" + runtime.GOOS
	}
	driver := filepath.Join(dir, "fern")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", host, "-o", driver, filepath.Join(dir, "fern.fern")).CombinedOutput(); err != nil {
		t.Fatalf("build compiler: %v\n%s", err, out)
	}
	src, wat := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wat")
	if err := os.WriteFile(src, []byte(e2eharness.WasiHTTPHandlerCensusSource(t, "../..", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(driver, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src, stdlib)
	cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SEM_IR=1", "FERN_SEM_IR_REPORT=1", "FERN_LEAKCHECK=1")
	report, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, report)
	}
	requireCompleteHTTPSemanticLowering(t, report)
	wit, err := filepath.Abs("../../cmd/fern/wit")
	if err != nil {
		t.Fatal(err)
	}
	core, embedded, component := filepath.Join(dir, "core.wasm"), filepath.Join(dir, "embedded.wasm"), filepath.Join(dir, "component.wasm")
	// External adapter stacks use their own pages, as in the primitive census.
	for _, args := range [][]string{
		{"parse", wat, "-o", core},
		{"component", "embed", wit, "-w", "fern", core, "-o", embedded},
		{"component", "new", embedded, "--realloc-via-memory-grow", "--adapt", "wasi_snapshot_preview1=" + adapter, "-o", component},
		{"validate", component},
	} {
		if out, err := exec.Command("wasm-tools", args...).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools %v: %v\n%s", args, err, out)
		}
	}
	out := e2eharness.RunWasiHTTPHandlerCensus(t, component, 32)
	allocs, frees, live := leakSummaryOf(t, "WASI HTTP handler", out)
	t.Logf("allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	if allocs != frees || live != 0 {
		t.Fatal("bounded WASI HTTP handler leaked")
	}
}
