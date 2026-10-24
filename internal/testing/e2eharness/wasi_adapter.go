package e2eharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
	wit := RepoPath("cmd", "fern", "wit")
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
