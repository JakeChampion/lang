package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/wasmbin"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	"github.com/jakechampion/lang/internal/wasm/component"
)

// buildAsyncProviderComponent compiles `src` (which must define `async function
// <srcFn>(): i32`) and lifts it as a provider component exporting
// `<exportName>: async func() -> u32` — the bring-your-own provider an
// `-async-provider` consumer bundles.
func buildAsyncProviderComponent(t *testing.T, src, srcFn, exportName string) []byte {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "prov.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		t.Fatalf("provider modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("provider constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("provider check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("provider monomorph: %v", err)
	}
	core, err := wasmbin.BuildWithOptions(prog, info, wasmbin.BuildOptions{
		Preview2WASI:    true,
		AsyncExportName: "__async_run",
		AsyncSourceFunc: srcFn,
	})
	if err != nil {
		t.Fatalf("provider build: %v", err)
	}
	return component.BuildAsyncLiftedExportComponent(core, "__async_run", exportName, component.CValtypeU32)
}

// TestAsyncProviderBundleCLI exercises `-async-provider`: the CLI bundles a
// bring-your-own provider component so a Fern program's single async `@import` is
// satisfied in-component, producing one self-contained runnable component.
//
// Provider: `compute: async func() -> u32` returning 42. Consumer:
//
//	@import("test:dep/d","compute") async function dep(): i32;
//	async function run(): i32 { return dep(); }
//
// The CLI's `run(... asyncProvider=provider.wasm ...)` must emit a component (not
// a bare core). When wasmtime is on PATH, `run()` returns the provider's 42.
func TestAsyncProviderBundleCLI(t *testing.T) {
	dir := t.TempDir()

	prov := buildAsyncProviderComponent(t,
		"async function compute(): i32 { return 42; }\nfunction main(): i32 { return 0; }\n",
		"compute", "compute")
	provPath := filepath.Join(dir, "provider.wasm")
	if err := os.WriteFile(provPath, prov, 0o644); err != nil {
		t.Fatal(err)
	}

	consumer := `@import("test:dep/d", "compute") async function dep(): i32;
async function run(): i32 { return dep(); }
function main(): i32 { return 0; }
`
	consumerPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "bundled.wasm")

	code, err := run(consumerPath, outPath, "wasm-bin", "", false, false, "qemu-aarch64",
		false /*componentWrap*/, false /*componentWrapCli*/, false /*asyncExport*/, provPath, false /*shared*/, "", nil)
	if err != nil || code != 0 {
		t.Fatalf("run(-async-provider): code=%d err=%v", code, err)
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	// A component starts with the wasm magic + the component layer/version
	// (0x0d 0x00 0x01 0x00), distinct from a core module's 0x01 0x00 0x00 0x00.
	if len(out) < 8 || !bytes.Equal(out[:4], []byte("\x00asm")) || out[6] != 0x01 {
		t.Fatalf("output is not a component (magic/version = % x)", out[:min(8, len(out))])
	}

	// Runtime check when wasmtime is available: the bundled provider's 42 flows out.
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; emitted a valid component but skipping runtime check")
	}
	res, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (bundled async provider): %v\n%s", err, res)
	}
	if !bytes.Contains(res, []byte("42")) {
		t.Errorf("bundled async provider: got %q, want 42", bytes.TrimSpace(res))
	}
}
