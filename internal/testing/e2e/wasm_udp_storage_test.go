package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestWasmUDPLifecycleCensus(t *testing.T) {
	for _, data := range []string{"", "x", "abcdefgh"} {
		t.Run(data, func(t *testing.T) {
			src, received := e2eharness.WasiUDPCensusProbe(t, data)
			component := buildLeakCheckCLIComponent(t, src, false)
			e2eharness.CheckWasiSocketCensus(t, component)
			received()
		})
	}
}

func TestWasmUDPGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, host := range []struct{ name, addr string }{
		{"0.0.0.0", "[0u8, 0u8, 0u8, 0u8]"},
		{"127.0.0.1", "[127u8, 0u8, 0u8, 1u8]"},
	} {
		for _, data := range []string{"", "x", "abcdefgh"} {
			t.Run(host.name+"/"+data, func(t *testing.T) {
				dir := t.TempDir()
				src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
				if err := os.WriteFile(src, []byte(e2eharness.WasiUDPStorageProbe(host.addr, data)), 0o644); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
					t.Fatalf("build UDP probe: %v\n%s", err, out)
				}
				e2eharness.CheckWasiSocketReclaim(t, wasm, "udp")
			})
		}
	}
}
