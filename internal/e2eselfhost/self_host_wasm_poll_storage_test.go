package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostWasmPollGuestStorage(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	for _, tc := range []struct {
		expr string
		ms   int
	}{{"wasm_poll(ps)", -1}, {"poll(ps, -1)", -1}, {"poll(ps, 0)", 0}, {"poll(ps, 5)", 5},
		{"poll(ps, 2147483647)", 2147483647}, {"poll(ps, 0 - 2147483647 - 1)", -2147483648}} {
		t.Run(tc.expr, func(t *testing.T) {
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader(e2eharness.WasiPollStorageProbe(tc.expr))
			wat, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "poll.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			e2eharness.CheckWasiPollStorage(t, path, tc.ms)
		})
	}
}

func TestSelfHostWasmPollLifecycleCensus(t *testing.T) {
	for _, expr := range []string{"wasm_poll(ps)", "poll(ps, -1)", "poll(ps, 0)", "poll(ps, 5)"} {
		t.Run(expr, func(t *testing.T) {
			component := buildWasiSocketCensusComponent(t, e2eharness.WasiPollCensusProbe(expr))
			e2eharness.CheckWasiSocketCensus(t, component)
		})
	}
}

func TestSelfHostWasmPollDeadlines(t *testing.T) {
	for _, tc := range e2eharness.WasiPollDeadlineCases() {
		t.Run(tc.Name, func(t *testing.T) {
			component := buildWasiSocketCensusComponent(t, tc.Source)
			e2eharness.CheckWasiSocketCensus(t, component)
		})
	}
}
