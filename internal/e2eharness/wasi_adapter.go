package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// AdaptPreview1Component turns a WASI core module (text or binary) whose stdio
// and exit go through wasi_snapshot_preview1 into a wasi:cli/run component of
// the `fern` world: the preview-1 adapter named by FERN_WASI_ADAPTER supplies
// that interface, and the core's own preview-2 imports (sockets, clocks, http)
// pass through. The composer in internal/wasm/component places preview-2
// imports only, so this is the one way to run such a core as a command.
//
// The adapter gets its own stack pages (--realloc-via-memory-grow): otherwise
// it calls the exported guest allocator before _start and holds two 64 KiB
// blocks for the component's lifetime, which the leak census would count.
func AdaptPreview1Component(t testing.TB, modulePath string) string {
	t.Helper()
	wasmtools, err := exec.LookPath("wasm-tools")
	if err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	adapter := os.Getenv("FERN_WASI_ADAPTER")
	if adapter == "" {
		t.Skip("FERN_WASI_ADAPTER unset")
	}
	wit, err := filepath.Abs(filepath.Join(repoRootDir, "cmd", "fern", "wit"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	core, embedded, component := filepath.Join(dir, "core.wasm"), filepath.Join(dir, "embedded.wasm"), filepath.Join(dir, "component.wasm")
	for _, args := range [][]string{
		{"parse", modulePath, "-o", core},
		{"component", "embed", wit, "-w", "fern", core, "-o", embedded},
		{"component", "new", embedded, "--realloc-via-memory-grow", "--adapt", "wasi_snapshot_preview1=" + adapter, "-o", component},
		{"validate", component},
	} {
		if out, err := exec.Command(wasmtools, args...).CombinedOutput(); err != nil {
			t.Fatalf("wasm-tools %v: %v\n%s", args, err, out)
		}
	}
	return component
}
