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
	for _, expr := range []string{"wasm_poll(ps)", "poll(ps, -1)", "poll(ps, 0)", "poll(ps, 5)"} {
		t.Run(expr, func(t *testing.T) {
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader(e2eharness.WasiPollStorageProbe(expr))
			wat, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "poll.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			e2eharness.CheckWasiPollStorage(t, path, expr == "poll(ps, 0)" || expr == "poll(ps, 5)")
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
