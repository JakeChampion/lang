package e2eharness

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStartWasmtimeServeReportsAnEarlyExit(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.wasm")
	srv, err := StartWasmtimeServe(Wasmtime(t), missing)
	if err == nil {
		srv.Stop()
		t.Fatalf("wasmtime serve listened on %s for a component that does not exist", srv.Addr)
	}
	for _, want := range []string{"exited before it listened", "failed to open"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
