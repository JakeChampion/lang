package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestWasmUDPLifecycleCensus(t *testing.T) {
	for _, data := range []string{"", "x", "abcdefgh"} {
		t.Run(data, func(t *testing.T) {
			src, received := e2eharness.WasiUDPCensusProbe(t, data)
			component := buildLeakCheckComponent(t, src, false)
			e2eharness.CheckWasiSocketCensus(t, component)
			received()
		})
	}
}

func TestWasmUDPGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "", "bad", "127.0.0.256"} {
		for _, data := range []string{"", "x", "abcdefgh"} {
			t.Run(host+"/"+data, func(t *testing.T) {
				dir := t.TempDir()
				src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
				if err := os.WriteFile(src, []byte(e2eharness.WasiUDPStorageProbe(host, data)), 0o644); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
					t.Fatalf("build UDP probe: %v\n%s", err, out)
				}
				operation := "udp"
				if host != "0.0.0.0" && host != "127.0.0.1" {
					operation = "udp-invalid"
				}
				e2eharness.CheckWasiSocketReclaim(t, wasm, operation)
			})
		}
	}
}
