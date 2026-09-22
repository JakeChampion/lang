package e2e

import (
	"github.com/jakechampion/lang/internal/e2eharness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWasmTCPSendLifecycleCensus(t *testing.T) {
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"inline", "x"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src, received := e2eharness.WasiTCPSendCensusProbe(t, tc.data)
			component := buildLeakCheckComponent(t, src, false)
			e2eharness.CheckWasiSocketCensus(t, component)
			received()
		})
	}
}

func TestWasmTCPSendGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"inline", "x"}, {"heap", "xxxxxxxx"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src := e2eharness.WasiStreamSendStorageProbe(tc.data)
			dir := t.TempDir()
			source, path := filepath.Join(dir, "send.fern"), filepath.Join(dir, "send.wasm")
			if err := os.WriteFile(source, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", path, source).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			operation := "send"
			if tc.name == "empty" || tc.name == "chunked" {
				operation += "-" + tc.name
			}
			e2eharness.CheckWasiSocketReclaim(t, path, operation)
		})
	}
}
