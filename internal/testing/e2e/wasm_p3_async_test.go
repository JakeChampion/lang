package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

var p3AsyncCoreModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x08, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x00, 0x00,
	0x02, 0x10, 0x01, 0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x03, 0x02, 0x01, 0x01,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x01,
	0x0a, 0x08, 0x01, 0x06, 0x00, 0x41, 0x2a, 0x10, 0x00, 0x0b,
}

func TestWasmP3AsyncExportAssembly(t *testing.T) {
	skipIfPreview2Missing(t)

	buf := component.PutComponentHeader(nil)
	buf = component.PutCanonTaskReturnSingle(buf, component.CValtypeU32)
	buf = component.PutCoreModuleSection(buf, p3AsyncCoreModule)
	buf = component.PutCoreInstanceSectionFromOneFuncExport(buf, "task-return", 0)
	buf = component.PutCoreInstanceSectionInstantiateWithInstanceArgs(buf, 0, []string{""}, []uint32{0})
	buf = component.PutAliasSectionCoreExportFunc(buf, 1, "run")
	buf = component.PutTypeSectionOneFuncAsync(buf, nil, nil, component.CValtypeU32)
	buf = component.PutCanonSectionLiftAsync(buf, 1, 0)
	buf = component.PutExportSectionOneFunc(buf, "run", 0)

	dir := t.TempDir()
	p := filepath.Join(dir, "p3async.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("async export: got %q, want 42", bytes.TrimSpace(out))
	}
}

func TestWasmP3NestedComponentReExport(t *testing.T) {
	skipIfPreview2Missing(t)

	provider := component.BuildAsyncLiftedExportComponent(p3AsyncCoreModule, "run", "dep", component.CValtypeU32)

	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentSection(buf, provider)
	buf = component.PutInstanceSectionInstantiateComponent(buf, 0)
	buf = component.PutAliasSectionInstanceExportFunc(buf, 0, "dep")
	buf = component.PutExportSectionOneFunc(buf, "dep", 0)

	dir := t.TempDir()
	p := filepath.Join(dir, "nested.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "dep()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (nested async): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("nested re-exported async dep: got %q, want 42", bytes.TrimSpace(out))
	}
}

var p3AsyncConsumerCore = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x0d, 0x03, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x01, 0x7f, 0x01, 0x7f, 0x60, 0x00, 0x00,
	0x02, 0x28, 0x03,
	0x00, 0x0b, 't', 'a', 's', 'k', '-', 'r', 'e', 't', 'u', 'r', 'n', 0x00, 0x00,
	0x00, 0x09, 'd', 'e', 'p', '-', 'l', 'o', 'w', 'e', 'r', 0x00, 0x01,
	0x03, 'm', 'e', 'm', 0x03, 'm', 'e', 'm', 0x02, 0x00, 0x01,
	0x03, 0x02, 0x01, 0x02,
	0x07, 0x07, 0x01, 0x03, 'r', 'u', 'n', 0x00, 0x02,
	0x0a, 0x10, 0x01, 0x0e, 0x00, 0x41, 0x08, 0x10, 0x01, 0x1a, 0x41, 0x08, 0x28, 0x02, 0x00, 0x10, 0x00, 0x0b,
}

var p3MemModule = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x07, 0x01, 0x03, 'm', 'e', 'm', 0x02, 0x00,
}

func TestWasmP3AsyncImportAwait(t *testing.T) {
	skipIfPreview2Missing(t)

	provider := component.BuildAsyncLiftedExportComponent(p3AsyncCoreModule, "run", "dep", component.CValtypeU32)

	inner := component.PutComponentHeader(nil)
	inner = component.PutTypeSectionOneFuncAsync(inner, nil, nil, component.CValtypeU32)
	inner = component.PutComponentImportSectionFuncs(inner, []string{"dep0"}, []uint32{0})
	inner = component.PutCoreModuleSection(inner, p3MemModule)
	inner = component.PutCoreInstanceSectionInstantiate(inner, 0)
	inner = component.PutAliasSectionCoreExport(inner, component.CoreSortMemory, 0, "mem")
	inner = component.PutCanonTaskReturnSingle(inner, component.CValtypeU32)
	inner = component.PutCanonSectionLowerAsync(inner, 0, 0)
	inner = component.PutCoreModuleSection(inner, p3AsyncConsumerCore)
	inner = component.PutCoreInstanceSectionFromExports(inner, []component.CoreInstanceExport{
		{Name: "task-return", Sort: component.CoreSortFunc, Idx: 0},
		{Name: "dep-lower", Sort: component.CoreSortFunc, Idx: 1},
	})
	inner = component.PutCoreInstanceSectionInstantiateWithInstanceArgs(inner, 1, []string{"", "mem"}, []uint32{1, 0})
	inner = component.PutAliasSectionCoreExportFunc(inner, 2, "run")
	inner = component.PutTypeSectionOneFuncAsync(inner, nil, nil, component.CValtypeU32)
	inner = component.PutCanonSectionLiftAsync(inner, 2, 1)
	inner = component.PutExportSectionOneFunc(inner, "run", 1)

	buf := component.PutComponentHeader(nil)
	buf = component.PutComponentSection(buf, provider)
	buf = component.PutInstanceSectionInstantiateComponent(buf, 0)
	buf = component.PutAliasSectionInstanceExportFunc(buf, 0, "dep")
	buf = component.PutComponentSection(buf, inner)
	buf = component.PutInstanceSectionInstantiateComponentWithFuncArgs(buf, 1, []string{"dep0"}, []uint32{0})
	buf = component.PutAliasSectionInstanceExportFunc(buf, 1, "run")
	buf = component.PutExportSectionOneFunc(buf, "run", 1)

	dir := t.TempDir()
	p := filepath.Join(dir, "await.wasm")
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("wasmtime", "run",
		"-W", "component-model-async,component-model-async-stackful",
		"--invoke", "run()", p).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run (async import await): %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("42")) {
		t.Errorf("async import await: got %q, want 42", bytes.TrimSpace(out))
	}
}
