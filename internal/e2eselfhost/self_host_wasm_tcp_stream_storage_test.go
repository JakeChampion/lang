package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostWasmTCPRecvLifecycleCensus(t *testing.T) {
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"short", "xxxxxxx"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src, sent := e2eharness.WasiTCPRecvCensusProbe(t, tc.data)
			component := buildWasiSocketCensusComponent(t, src)
			e2eharness.CheckWasiSocketCensus(t, component)
			sent()
		})
	}
}

func TestSelfHostWasmTCPRecvGuestStorage(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	for _, tc := range []struct {
		name string
		max  int
	}{{"recv", 3}, {"recv-zero", 0}, {"recv-negative", -1}} {
		t.Run(tc.name, func(t *testing.T) {
			src := e2eharness.WasiStreamRecvStorageProbe(tc.max)
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader(src)
			wat, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "send.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			e2eharness.CheckWasiSocketReclaim(t, path, tc.name)
		})
	}
}

func TestSelfHostWasmTCPSendLifecycleCensus(t *testing.T) {
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"inline", "x"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src, received := e2eharness.WasiTCPSendCensusProbe(t, tc.data)
			component := buildWasiSocketCensusComponent(t, src)
			e2eharness.CheckWasiSocketCensus(t, component)
			received()
		})
	}
}

func TestSelfHostWasmTCPSendGuestStorage(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")
	for _, tc := range []struct{ name, data string }{{"empty", ""}, {"inline", "x"}, {"heap", "xxxxxxxx"}, {"chunked", strings.Repeat("x", 4097)}} {
		t.Run(tc.name, func(t *testing.T) {
			src := e2eharness.WasiStreamSendStorageProbe(tc.data)
			cmd := runX86_64Bin(runner, bin)
			cmd.Stdin = strings.NewReader(src)
			wat, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "send.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			operation := "send"
			if tc.name == "empty" || tc.name == "chunked" {
				operation += "-" + tc.name
			}
			e2eharness.CheckWasiSocketReclaim(t, path, operation)
		})
	}
}
