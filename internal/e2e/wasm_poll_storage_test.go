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
	for _, tc := range []struct {
		expr string
		ms   int
	}{{"wasm_poll(ps)", -1}, {"poll(ps, -1)", -1}, {"poll(ps, 0)", 0}, {"poll(ps, 5)", 5},
		{"poll(ps, 2147483647)", 2147483647}, {"poll(ps, 0 - 2147483647 - 1)", -2147483648}} {
		t.Run(tc.expr, func(t *testing.T) {
			dir := t.TempDir()
			src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(src, []byte(e2eharness.WasiPollStorageProbe(tc.expr)), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			e2eharness.CheckWasiPollStorage(t, wasm, tc.ms)
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

func TestWasmPollDeadlines(t *testing.T) {
	for _, tc := range e2eharness.WasiPollDeadlineCases() {
		t.Run(tc.Name, func(t *testing.T) {
			component := buildLeakCheckComponent(t, tc.Source, false)
			e2eharness.CheckWasiSocketCensus(t, component)
		})
	}
}
