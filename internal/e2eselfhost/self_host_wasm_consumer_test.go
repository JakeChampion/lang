package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// selfHostRunIOCore compiles src through the wasm_runio_run driver, the
// self-host emitter of a run-io core (WAT out, wasi:cli/stdout imported, the
// program's @imports surfaced as core imports), and assembles it with
// wasm-tools into the core-module bytes the Go composer takes.
func selfHostRunIOCore(t *testing.T, gcc string, runner []string, driverBin, wasmtools, dir string, src []byte) []byte {
	t.Helper()
	wat := runCapture(t, gcc, runner, driverBin, src)
	watPath := filepath.Join(dir, "consumer_core.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write consumer wat: %v", err)
	}
	corePath := filepath.Join(dir, "consumer_core.wasm")
	if out, err := exec.Command(wasmtools, "parse", watPath, "-o", corePath).CombinedOutput(); err != nil {
		t.Fatalf("wasm-tools parse consumer core: %v\n%s", err, out)
	}
	core, err := os.ReadFile(corePath)
	if err != nil {
		t.Fatalf("read consumer core: %v", err)
	}
	return core
}
