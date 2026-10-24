package component_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

func helloCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x01, 0x7f, 0x00,
		0x02, 0x12,
		0x01,
		0x09, 'w', 'a', 's', 'i', '-', 'e', 'x', 'i', 't',
		0x04, 'e', 'x', 'i', 't',
		0x00, 0x00,
	}
}

func TestWrapWasiImported_ExitOnly(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	core := helloCoreModule()
	comp := component.WrapWasiImported(core, []component.WasiImport{
		{
			InterfaceName:    "wasi:cli/exit@0.2.0",
			FuncName:         "exit",
			ParamNames:       []string{"code"},
			ParamValtypes:    []byte{component.CValtypeU32},
			CoreImportModule: "wasi-exit",
		},
	})

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hex := bytes.Buffer{}
		for _, b := range comp {
			fmt.Fprintf(&hex, "%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\noutput: %s\nbytes:\n%s", err, out, hex.String())
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"\"exit\"",
		"\"code\"",
		"alias export",
		"canon lower",
		"(core module",
		"with \"wasi-exit\"",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, got)
		}
	}
}

func addCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x07, 0x01, 0x03, 'a', 'd', 'd', 0x00, 0x00,
		0x0a, 0x09, 0x01, 0x07, 0x00,
		0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
	}
}

func TestBuildLiftedExportComponent_RunsUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	core := addCoreModule()
	comp := component.BuildLiftedExportComponent(
		core, "add", "add",
		[]string{"a", "b"},
		[]byte{component.CValtypeU32, component.CValtypeU32},
		component.CValtypeU32,
	)

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hex := bytes.Buffer{}
		for _, b := range comp {
			fmt.Fprintf(&hex, "%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\noutput: %s\nbytes:\n%s", err, out, hex.String())
	}
	out, err := exec.Command("wasmtime", "run", "--invoke", "add(3, 4)", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasmtime run failed: %v\noutput:\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "7" {
		t.Errorf("wasmtime stdout = %q, want %q", got, "7")
	}
}

func runCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
		0x03, 0x02, 0x01, 0x00,
		0x07, 0x08, 0x01, 0x04, '_', 'r', 'u', 'n', 0x00, 0x00,
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
	}
}

func TestBuildWasiCliRunComponent_RunsUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	core := runCoreModule()
	comp := component.BuildWasiCliRunComponent(core, "_run")

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hex := bytes.Buffer{}
		for _, b := range comp {
			fmt.Fprintf(&hex, "%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\noutput: %s\nbytes:\n%s", err, out, hex.String())
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:cli/run@0.2.0",
		"canon lift",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
	if out, err := exec.Command("wasmtime", "run", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
}

func TestRawInstanceTypeBody_EscapeHatch(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	core := helloCoreModule()
	rawBody := []byte{
		0x01, 0x42, 0x03,
		0x01, 0x6a, 0x00, 0x00,
		0x01, 0x40, 0x01,
		0x06, 's', 't', 'a', 't', 'u', 's', 0x00,
		0x01, 0x00,
		0x04,
		0x00, 0x04, 'e', 'x', 'i', 't',
		0x01, 0x01,
	}
	comp := component.WrapWasiImported(core, []component.WasiImport{
		{
			InterfaceName:       "wasi:cli/exit@0.2.0",
			FuncName:            "exit",
			CoreImportModule:    "wasi-exit",
			RawInstanceTypeBody: rawBody,
		},
	})

	dir := t.TempDir()
	compPath := filepath.Join(dir, "out.wasm")
	if err := os.WriteFile(compPath, comp, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hex := bytes.Buffer{}
		for _, b := range comp {
			fmt.Fprintf(&hex, "%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\noutput: %s\nbytes:\n%s", err, out, hex.String())
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"\"status\"",
		"(result)",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}

func TestRawInstanceTypeBody_ResourceDecl(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	buf := component.PutComponentHeader(nil)
	buf = component.PutTypeSectionRawBody(buf, []byte{
		0x01, 0x42, 0x01,
		0x04, 0x00, 0x05, 'e', 'r', 'r', 'o', 'r', 0x03, 0x01,
	})

	dir := t.TempDir()
	compPath := filepath.Join(dir, "resource.wasm")
	if err := os.WriteFile(compPath, buf, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("wasm-tools", "validate", compPath).CombinedOutput(); err != nil {
		hex := bytes.Buffer{}
		for _, b := range buf {
			fmt.Fprintf(&hex, "%02x ", b)
		}
		t.Fatalf("wasm-tools validate failed: %v\noutput: %s\nbytes:\n%s", err, out, hex.String())
	}
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"\"error\"",
		"(sub resource)",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}
