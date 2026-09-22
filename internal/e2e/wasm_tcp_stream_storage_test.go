package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestWasmTCPRecvLifecycleCensus(t *testing.T) {
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"short", "xxxxxxx"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src, sent := e2eharness.WasiTCPRecvCensusProbe(t, tc.data)
			component := buildLeakCheckComponent(t, src, false)
			e2eharness.CheckWasiSocketCensus(t, component)
			sent()
		})
	}
}

func TestWasmTCPRecvGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range []struct {
		name string
		max  int
	}{{"recv", 3}, {"recv-zero", 0}, {"recv-negative", -1}} {
		t.Run(tc.name, func(t *testing.T) {
			src := e2eharness.WasiStreamRecvStorageProbe(tc.max)
			dir := t.TempDir()
			source, path := filepath.Join(dir, "send.fern"), filepath.Join(dir, "send.wasm")
			if err := os.WriteFile(source, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", path, source).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			e2eharness.CheckWasiSocketReclaim(t, path, tc.name)
		})
	}
}

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
