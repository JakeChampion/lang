package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

func TestWasmP3AsyncListParamExportProvider(t *testing.T) {
	skipIfPreview2Missing(t)

	comp := component.BuildAsyncLiftedExportComponentListParam(p3SendStringCore, "mem", "cabi_realloc", "send", "recv", component.CValtypeU8, component.CValtypeU32)
	dir := t.TempDir()
	p := filepath.Join(dir, "recvparam.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "recv([104,101,108,108,111])", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list param): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("5")) {
		t.Errorf("async list param: got %q, want 5 (len of 5-byte list)", bytes.TrimSpace(out))
	}
}
