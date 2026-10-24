package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

var p3FetchStringCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x09, 0x02, 0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60, 0x00, 0x00,
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x03, 0x02, 0x01, 0x01,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x0f, 0x02, 0x03, 'm', 'e', 'm', 0x02, 0x00, 0x05, 'f', 'e', 't', 'c', 'h', 0x00, 0x01,
	0x0a, 0x0a, 0x01, 0x08, 0x00, 0x41, 0x08, 0x41, 0x05, 0x10, 0x00, 0x0b,
	0x0b, 0x0b, 0x01, 0x00, 0x41, 0x08, 0x0b, 0x05, 'h', 'e', 'l', 'l', 'o',
}

func TestWasmP3AsyncStringExportProvider(t *testing.T) {
	skipIfPreview2Missing(t)

	comp := component.BuildAsyncLiftedExportComponentString(p3FetchStringCore, "mem", "fetch", "fetch")
	dir := t.TempDir()
	p := filepath.Join(dir, "strprov.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", `fetch()`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async string export): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("hello")) {
		t.Errorf("async string export: got %q, want hello", bytes.TrimSpace(out))
	}
}

func TestWasmP3AsyncListExportProvider(t *testing.T) {
	skipIfPreview2Missing(t)

	comp := component.BuildAsyncLiftedExportComponentList(p3FetchStringCore, "mem", "fetch", "fetch", component.CValtypeU8)
	dir := t.TempDir()
	p := filepath.Join(dir, "listprov.wasm")
	if err := os.WriteFile(p, comp, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", `fetch()`, p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async list export): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("104")) || !bytes.Contains(out, []byte("111")) {
		t.Errorf("async list export: got %q, want bytes of \"hello\" (104..111)", bytes.TrimSpace(out))
	}
}
