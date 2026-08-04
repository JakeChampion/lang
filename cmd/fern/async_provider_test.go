package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err := constfold.Fold(prog, nil); err != nil {
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
		false /*componentWrap*/, false /*componentWrapCli*/, false /*asyncExport*/, []string{provPath}, false /*shared*/, "", false /*optimize*/, nil)
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

// buildAsyncProviderComponentParams is buildAsyncProviderComponent for a provider
// that TAKES scalar params: compiles `async function <srcFn>(<params>): i32` and
// lifts it as `<exportName>: async func(<params>) -> u32`. (The wasmbin
// -async-export wrapper now forwards source-function params, so a param-taking
// provider is authored in Fern rather than a hand-built core.)
func buildAsyncProviderComponentParams(t *testing.T, src, srcFn, exportName string, paramNames []string, paramVals []byte) []byte {
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
	if err := constfold.Fold(prog, nil); err != nil {
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
	return component.BuildAsyncLiftedExportComponentParams(core, "__async_run", exportName, paramNames, paramVals, component.CValtypeU32)
}

// TestAsyncProviderBundleParamsCLI widens TestAsyncProviderBundleCLI to a
// scalar-PARAM async import: `add: async func(a, b: u32) -> u32` returning a+b.
// Consumer:
//
//	@import("test:dep/d","add") async function add(a: i32, b: i32): i32;
//	async function run(): i32 { return add(20, 22); }
//
// `-async-provider` flattens the import's scalar params into the canon-lower
// signature; the bundled provider (authored in Fern) computes 20+22 → 42.
func TestAsyncProviderBundleParamsCLI(t *testing.T) {
	dir := t.TempDir()

	prov := buildAsyncProviderComponentParams(t,
		"async function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return 0; }\n",
		"add", "add", []string{"a", "b"}, []byte{component.CValtypeU32, component.CValtypeU32})
	provPath := filepath.Join(dir, "provider.wasm")
	if err := os.WriteFile(provPath, prov, 0o644); err != nil {
		t.Fatal(err)
	}

	consumer := `@import("test:dep/d", "add") async function add(a: i32, b: i32): i32;
async function run(): i32 { return add(20, 22); }
function main(): i32 { return 0; }
`
	consumerPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "bundled.wasm")

	code, err := run(consumerPath, outPath, "wasm-bin", "", false, false, "qemu-aarch64",
		false, false, false, []string{provPath}, false, "", false, nil)
	if err != nil || code != 0 {
		t.Fatalf("run(-async-provider, params): code=%d err=%v", code, err)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 8 || !bytes.Equal(out[:4], []byte("\x00asm")) || out[6] != 0x01 {
		t.Fatalf("output is not a component (magic/version = % x)", out[:min(8, len(out))])
	}

	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; emitted a valid component but skipping runtime check")
	}
	res, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (bundled async provider, params): %v\n%s", err, res)
	}
	if !bytes.Contains(res, []byte("42")) {
		t.Errorf("bundled async provider (params): got %q, want 42 (20+22)", bytes.TrimSpace(res))
	}
}

// TestAsyncProviderBundleMultiCLI covers bundling MULTIPLE async imports, each
// mapped to its own provider via `-async-provider WITNAME=PATH` (repeatable):
//
//	@import("test:a/d","one") async function one(): i32;   // provider returns 20
//	@import("test:b/d","two") async function two(): i32;   // provider returns 22
//	async function run(): i32 { return one() + two(); }    // -> 42
func TestAsyncProviderBundleMultiCLI(t *testing.T) {
	dir := t.TempDir()

	p1 := buildAsyncProviderComponent(t,
		"async function compute(): i32 { return 20; }\nfunction main(): i32 { return 0; }\n",
		"compute", "one")
	p2 := buildAsyncProviderComponent(t,
		"async function compute(): i32 { return 22; }\nfunction main(): i32 { return 0; }\n",
		"compute", "two")
	p1Path := filepath.Join(dir, "one.wasm")
	p2Path := filepath.Join(dir, "two.wasm")
	if err := os.WriteFile(p1Path, p1, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2Path, p2, 0o644); err != nil {
		t.Fatal(err)
	}

	consumer := `@import("test:a/d", "one") async function one(): i32;
@import("test:b/d", "two") async function two(): i32;
async function run(): i32 { return one() + two(); }
function main(): i32 { return 0; }
`
	consumerPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "bundled.wasm")

	code, err := run(consumerPath, outPath, "wasm-bin", "", false, false, "qemu-aarch64",
		false, false, false, []string{"one=" + p1Path, "two=" + p2Path}, false, "", false, nil)
	if err != nil || code != 0 {
		t.Fatalf("run(-async-provider, multi): code=%d err=%v", code, err)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 8 || !bytes.Equal(out[:4], []byte("\x00asm")) || out[6] != 0x01 {
		t.Fatalf("output is not a component (magic/version = % x)", out[:min(8, len(out))])
	}

	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; emitted a valid component but skipping runtime check")
	}
	res, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (multi async provider): %v\n%s", err, res)
	}
	if !bytes.Contains(res, []byte("42")) {
		t.Errorf("multi async provider: got %q, want 42 (20+22)", bytes.TrimSpace(res))
	}
}

// TestAsyncProviderBundleMissingCLI checks the clear error when an async import
// has no matching -async-provider entry.
func TestAsyncProviderBundleMissingCLI(t *testing.T) {
	dir := t.TempDir()
	p1 := buildAsyncProviderComponent(t,
		"async function compute(): i32 { return 20; }\nfunction main(): i32 { return 0; }\n",
		"compute", "one")
	p1Path := filepath.Join(dir, "one.wasm")
	if err := os.WriteFile(p1Path, p1, 0o644); err != nil {
		t.Fatal(err)
	}
	consumer := `@import("test:a/d", "one") async function one(): i32;
@import("test:b/d", "two") async function two(): i32;
async function run(): i32 { return one() + two(); }
function main(): i32 { return 0; }
`
	consumerPath := filepath.Join(dir, "consumer.fern")
	if err := os.WriteFile(consumerPath, []byte(consumer), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only `one` provided; `two` is unmapped → error mentioning two.
	_, err := run(consumerPath, filepath.Join(dir, "o.wasm"), "wasm-bin", "", false, false, "qemu-aarch64",
		false, false, false, []string{"one=" + p1Path}, false, "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "two") {
		t.Fatalf("expected a 'no provider for two' error, got %v", err)
	}
}

// TestAsyncExportParamsCLI covers `fern -target wasm-bin` lifting a param'd async
// function as a component export: `async function add(a: i32, b: i32): i32` →
// `add: async func(a, b: u32) -> u32`. The CLI now selects the param-aware lift
// (BuildAsyncLiftedExportComponentParams) when the async source has parameters,
// and the wasmbin wrapper forwards them. Emits a component; runs add(20,22) → 42
// when wasmtime is on PATH.
func TestAsyncExportParamsCLI(t *testing.T) {
	dir := t.TempDir()
	src := `async function add(a: i32, b: i32): i32 { return a + b; }
function main(): i32 { return 0; }
`
	srcPath := filepath.Join(dir, "add.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "add.wasm")

	code, err := run(srcPath, outPath, "wasm-bin", "", false, false, "qemu-aarch64",
		false, false, true /*asyncExport*/, nil /*asyncProviders*/, false, "", false, nil)
	if err != nil || code != 0 {
		t.Fatalf("run(-async-export, params): code=%d err=%v", code, err)
	}
	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 8 || !bytes.Equal(out[:4], []byte("\x00asm")) || out[6] != 0x01 {
		t.Fatalf("output is not a component (magic/version = % x)", out[:min(8, len(out))])
	}

	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; emitted a valid component but skipping runtime check")
	}
	res, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "add(20, 22)", outPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (CLI param async export): %v\n%s", err, res)
	}
	if !bytes.Contains(res, []byte("42")) {
		t.Errorf("CLI param async export: got %q, want 42", bytes.TrimSpace(res))
	}
}
