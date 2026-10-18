package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostWasmUDPLifecycleCensus(t *testing.T) {
	for _, data := range []string{"", "x", "abcdefgh"} {
		t.Run(data, func(t *testing.T) {
			src, received := e2eharness.WasiUDPCensusProbe(t, data)
			component := buildWasiSocketCensusComponent(t, src)
			e2eharness.CheckWasiSocketCensus(t, component)
			received()
		})
	}
}

func TestSelfHostWasmUDPGuestStorage(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")
	for _, host := range []struct{ name, addr string }{
		{"0.0.0.0", "[0u8, 0u8, 0u8, 0u8]"},
		{"127.0.0.1", "[127u8, 0u8, 0u8, 1u8]"},
	} {
		for _, data := range []string{"", "x", "abcdefgh"} {
			t.Run(host.name+"/"+data, func(t *testing.T) {
				cmd := runX86_64Bin(runner, bin)
				cmd.Stdin = strings.NewReader(e2eharness.WasiUDPStorageProbe(host.addr, data))
				wat, err := cmd.Output()
				if err != nil {
					t.Fatalf("self-host UDP probe: %v", err)
				}
				path := filepath.Join(t.TempDir(), "udp.wat")
				if err := os.WriteFile(path, wat, 0o644); err != nil {
					t.Fatal(err)
				}
				e2eharness.CheckWasiSocketReclaim(t, path, "udp")
			})
		}
	}
}
