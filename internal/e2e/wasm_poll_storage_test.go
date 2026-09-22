package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestWasmPollGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, expr := range []string{"wasm_poll(ps)", "poll(ps, -1)", "poll(ps, 0)", "poll(ps, 5)"} {
		t.Run(expr, func(t *testing.T) {
			dir := t.TempDir()
			src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(src, []byte(e2eharness.WasiPollStorageProbe(expr)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			// Bootstrap poll currently forwards all timeouts to wasm_poll.
			e2eharness.CheckWasiPollStorage(t, wasm, false)
		})
	}
}

func TestWasmPollLifecycleCensus(t *testing.T) {
	for _, expr := range []string{"wasm_poll(ps)", "poll(ps, -1)", "poll(ps, 0)", "poll(ps, 5)"} {
		t.Run(expr, func(t *testing.T) {
			component := buildLeakCheckComponent(t, e2eharness.WasiPollCensusProbe(expr), false)
			e2eharness.CheckWasiSocketCensus(t, component)
		})
	}
}
