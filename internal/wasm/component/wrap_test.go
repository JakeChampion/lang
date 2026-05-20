package component_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/wasm/component"
)

// helloCoreModule returns the bytes of a tiny core wasm module
// that imports a host function `(import "wasi-exit" "exit"
// (func (param i32)))`. Hand-rolled so the test doesn't depend on
// the rest of the wasm encoder packages.
//
// Section layout:
//
//	preamble (8 bytes)
//	type section: (func (param i32))
//	import section: "wasi-exit"."exit" of type 0
func helloCoreModule() []byte {
	return []byte{
		// magic + version 1
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section (id 1), size 5; body:
		//   vec(1) functypes, functype 0x60,
		//   vec(1) param i32 (0x7f), vec(0) results
		0x01, 0x05, 0x01, 0x60, 0x01, 0x7f, 0x00,
		// import section (id 2), size 18; body:
		//   vec(1), module name "wasi-exit" (len 9), import name
		//   "exit" (len 4), import kind = func, typeidx 0
		0x02, 0x12,
		0x01,
		0x09, 'w', 'a', 's', 'i', '-', 'e', 'x', 'i', 't',
		0x04, 'e', 'x', 'i', 't',
		0x00, 0x00,
	}
}

// TestWrapWasiImported_ExitOnly drives the most basic shape:
// a core module that imports wasi-exit.exit, wrapped as a
// preview-2 component importing wasi:cli/exit@0.2.0.
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
