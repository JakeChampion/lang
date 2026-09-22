package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestWasmSocketSetupReclaimsOnError(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"udp", "udp_send(\"127.0.0.1\", 1, \"x\")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			if err := os.WriteFile(src, []byte("function main(): i32 { return "+tc.expr+"; }"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
				t.Fatalf("build socket probe: %v\n%s", err, out)
			}
			e2eharness.CheckWasiSocketReclaim(t, wasm, tc.name)
		})
	}
}

func TestWasmSocketCloseZeroHandles(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"accept", "tcp_accept(0)"}, // harness supplies a borrowed listener record
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			body := "function main(): i32 { var h: i32 = " + tc.expr + "; if (h < 0) { return h; } return tcp_close(h); }"
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
				t.Fatalf("build socket probe: %v\n%s", err, out)
			}
			e2eharness.CheckWasiSocketReclaim(t, wasm, tc.name+"-close")
		})
	}
}

func TestWasmSocketGuestStorage(t *testing.T) {
	fern := e2eharness.BuildLangBinForInterp(t)
	for _, tc := range []struct{ name, expr string }{
		{"listen", "tcp_listen(0)"},
		{"connect", "tcp_connect(0, 1)"},
		{"accept", "tcp_accept(0)"}, // harness supplies a borrowed listener record
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src, wasm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.wasm")
			body := e2eharness.WasiSocketStorageProbe(tc.expr)
			if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(fern, "-target", "wasm32-wasi", "-emit", "core-module", "-o", wasm, src).CombinedOutput(); err != nil {
				t.Fatalf("build socket probe: %v\n%s", err, out)
			}
			e2eharness.CheckWasiSocketReclaim(t, wasm, tc.name+"-close")
		})
	}
}
