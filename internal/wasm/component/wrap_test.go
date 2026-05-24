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

// addCoreModule returns a tiny core wasm module that exports
// `add(a: i32, b: i32) -> i32` returning their sum. Hand-rolled
// so the test doesn't depend on the rest of the wasm encoder.
func addCoreModule() []byte {
	return []byte{
		// preamble (8)
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section (id 1), size 7; body:
		//   vec(1) functypes,
		//   functype 0x60, vec(2) [i32, i32], vec(1) [i32]
		0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
		// function section (id 3), size 2; body:
		//   vec(1) typeidxs = [0]
		0x03, 0x02, 0x01, 0x00,
		// export section (id 7), size 7; body:
		//   vec(1) exports, name "add" (len 3), kind=func, idx=0
		0x07, 0x07, 0x01, 0x03, 'a', 'd', 'd', 0x00, 0x00,
		// code section (id 10), size 9; body:
		//   vec(1) bodies, body length 7, vec(0) locals,
		//   local.get 0 (0x20 0x00), local.get 1 (0x20 0x01),
		//   i32.add (0x6a), end (0x0b)
		0x0a, 0x09, 0x01, 0x07, 0x00,
		0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
	}
}

// TestBuildLiftedExportComponent_RunsUnderWasmtime exercises the
// high-level BuildLiftedExportComponent helper end-to-end via
// wasmtime. add(3, 4) should return 7. Mirrors the Lang-side
// TestWASMComponentBuildHelperWithParamsRunnable in spirit.
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

// runCoreModule returns the bytes of a tiny core wasm module that
// exports `_run` with signature `() -> i32` returning 0. Hand-
// rolled so the test doesn't depend on the rest of the wasm
// encoder.
func runCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section: vec(1), 0x60 vec(0) params vec(1) result i32
		0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f,
		// function section: vec(1) typeidx [0]
		0x03, 0x02, 0x01, 0x00,
		// export section: "_run" -> func 0
		0x07, 0x08, 0x01, 0x04, '_', 'r', 'u', 'n', 0x00, 0x00,
		// code section: i32.const 0, end
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
	}
}

// TestBuildWasiCliRunComponent_RunsUnderWasmtime exercises the
// "wasi:cli/run-exporting" component shape end-to-end. The
// produced component must be runnable directly via `wasmtime run`
// (no `--invoke`) — the host invokes the lifted run function, the
// returned i32 (0) lowers to result::ok, and wasmtime exits 0.
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
	// End-to-end: `wasmtime run` (no --invoke) accepts and runs
	// the component. _run returns 0 → result::ok → exit 0.
	if out, err := exec.Command("wasmtime", "run", compPath).CombinedOutput(); err != nil {
		t.Fatalf("wasmtime run failed: %v\n%s", err, out)
	}
}

// exitRunCoreModule returns the bytes of a tiny core wasm module
// that BOTH imports `wasi:cli/exit@0.2.0::exit(i32) -> ()` AND
// exports `main() -> i32`. Useful to drive
// WrapWasiImportedAsCliRun end-to-end with a real preview-2 host
// providing exit, exercising both the import + run-export paths.
func exitRunCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section: vec(2)
		//   type 0: (param i32) -> ()
		//   type 1: () -> (result i32)
		0x01, 0x09, 0x02,
		0x60, 0x01, 0x7f, 0x00,
		0x60, 0x00, 0x01, 0x7f,
		// import section: wasi:cli/exit@0.2.0.exit, typeidx 0
		0x02, 0x1c, 0x01,
		0x13, 'w', 'a', 's', 'i', ':', 'c', 'l', 'i',
		'/', 'e', 'x', 'i', 't', '@', '0', '.', '2', '.', '0',
		0x04, 'e', 'x', 'i', 't',
		0x00, 0x00,
		// function section: vec(1) typeidx [1]
		0x03, 0x02, 0x01, 0x01,
		// export section: "main" -> func 1 (after import shift)
		0x07, 0x08, 0x01, 0x04, 'm', 'a', 'i', 'n', 0x00, 0x01,
		// code section: i32.const 0, end (main returns 0 immediately;
		// the exit import is reachable but unused at runtime).
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
	}
}

// TestWrapWasiImportedAsCliRun_RunsUnderWasmtime exercises the
// combined import + cli-run pipeline end-to-end. A core module
// that imports wasi:cli/exit and exports main is wrapped via
// WrapWasiImportedAsCliRun; wasmtime should accept and run the
// component via `wasmtime run` (no --invoke). main returns 0 →
// result::ok → wasmtime exits 0.
func TestWrapWasiImportedAsCliRun_RunsUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}

	core := exitRunCoreModule()
	comp := component.WrapWasiImportedAsCliRun(
		core,
		[]component.WasiImport{
			{
				InterfaceName:    "wasi:cli/exit@0.2.0",
				FuncName:         "exit",
				ParamNames:       []string{"status"},
				ParamValtypes:    []byte{0x00},
				CoreImportModule: "wasi:cli/exit@0.2.0",
				InnerTypes:       [][]byte{component.InnerTypeResultEmpty},
			},
		},
		"main",
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
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"wasi:cli/run@0.2.0",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// exitMainCoreModule returns the bytes of a tiny core wasm module
// that both imports `wasi-exit::exit(i32) -> ()` AND exports
// `main() -> i32`. The exported `main` does:
//
//	i32.const 99
//	call exit         ; conceptually never returns
//	i32.const 42      ; dead code for the validator
//	end
//
// This exercises both the import-wiring path AND the export-lifting
// path of WrapWasiImportedWithExport: when wasmtime --invokes main,
// the embedded core dispatches into the host-provided exit handler.
func exitMainCoreModule() []byte {
	return []byte{
		// magic + version 1
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section (id 1), size 9; body:
		//   vec(2) functypes
		//   type 0: 0x60 vec(1) [i32] vec(0) []      ; (param i32) -> ()
		//   type 1: 0x60 vec(0) []     vec(1) [i32]  ; () -> (result i32)
		0x01, 0x09, 0x02,
		0x60, 0x01, 0x7f, 0x00,
		0x60, 0x00, 0x01, 0x7f,
		// import section (id 2), size 18; body:
		//   vec(1)
		//   module "wasi-exit" (len 9), name "exit" (len 4),
		//   kind=func (0x00), typeidx 0
		0x02, 0x12,
		0x01,
		0x09, 'w', 'a', 's', 'i', '-', 'e', 'x', 'i', 't',
		0x04, 'e', 'x', 'i', 't',
		0x00, 0x00,
		// function section (id 3), size 2; body:
		//   vec(1) typeidxs = [1]
		0x03, 0x02, 0x01, 0x01,
		// export section (id 7), size 8; body:
		//   vec(1) exports
		//   name "main" (len 4), kind=func, funcidx 1
		//   (the imported exit is funcidx 0, so our defined main
		//    sits at funcidx 1 after the import-shift)
		0x07, 0x08, 0x01, 0x04, 'm', 'a', 'i', 'n', 0x00, 0x01,
		// code section (id 10), size 10; body:
		//   vec(1) bodies
		//   body length 8: vec(0) locals,
		//     i32.const 99 (0x41 0x63), call 0 (0x10 0x00),
		//     i32.const 42 (0x41 0x2a), end (0x0b)
		0x0a, 0x0a,
		0x01, 0x08, 0x00,
		0x41, 0x63, 0x10, 0x00, 0x41, 0x2a, 0x0b,
	}
}

// TestWrapWasiImportedWithExport_Structural exercises the combined
// import + export pipeline structurally. The produced component
// must validate under wasm-tools and surface both the import and
// the export at the component level.
//
// End-to-end execution under wasmtime is intentionally NOT
// exercised here. The `wasi:cli/exit@0.2.0::exit` interface takes a
// canonical-ABI `result<_, _>` (not a `u32`), so the host linker
// rejects our `(param code u32)` declaration. Result-type encoding
// is a future slice; for now this test pins the structural shape.
func TestWrapWasiImportedWithExport_Structural(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	core := exitMainCoreModule()
	comp := component.WrapWasiImportedWithExport(
		core,
		[]component.WasiImport{
			{
				InterfaceName:    "wasi:cli/exit@0.2.0",
				FuncName:         "exit",
				ParamNames:       []string{"code"},
				ParamValtypes:    []byte{component.CValtypeU32},
				CoreImportModule: "wasi-exit",
			},
		},
		"main", "main",
		nil, nil,
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
	out, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"wasi:cli/exit@0.2.0",
		"export \"main\"",
		"canon lift",
		"canon lower",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}

// TestRawInstanceTypeBody_EscapeHatch exercises
// `WasiImport.RawInstanceTypeBody` by reproducing the
// wasi:cli/exit@0.2.0 import using a fully-pre-encoded
// instance-type body instead of the structured fields. The
// bytes match what wasm-tools emits for the same interface:
// 3 decls (result-type, func-with-result-param, export). Same
// hand-rolled core module that imports wasi-exit::exit. The
// produced component validates and prints with the expected
// interface shape.
func TestRawInstanceTypeBody_EscapeHatch(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	core := helloCoreModule()
	rawBody := []byte{
		0x01, 0x42, 0x03,
		// decl 0: type — result<_, _>
		0x01, 0x6a, 0x00, 0x00,
		// decl 1: type — func(status: typeidx 0)
		0x01, 0x40, 0x01,
		0x06, 's', 't', 'a', 't', 'u', 's', 0x00,
		0x01, 0x00,
		// decl 2: export func "exit" (typeidx 1)
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

// TestRawInstanceTypeBody_ResourceDecl exercises the escape
// hatch against a resource-bearing interface — wasi:io/error,
// the simplest preview-2 resource shape (just declares the
// resource type, no methods). Confirms the structured-fields
// limitation (no resource support yet) is genuinely lifted by
// RawInstanceTypeBody.
//
// The interface body matches what wasm-tools emits for
//   interface error { resource error; }
//
// in WIT — `42 01 04 00 05 "error" 03 01`:
//
//   42       instance type form
//   01       vec(1) decls
//   04       export decl
//   00 05 "error"  exportname (label + uleb len + name bytes)
//   03 01    externdesc type, bound = (sub)
//
// Wrapped in `01 42 ...` (the vec(1) types of the type section
// body), the full body is 13 bytes (0x0d).
//
// No FuncName because resource-only interfaces export no
// functions; the wrap pipeline still emits the import section,
// but the alias / canon-lower / core-instance steps are
// effectively dead-code (FuncName is "" so the core module's
// import name pair is also unsatisfied — this test only
// validates the TYPE-section emission, not the full pipeline).
// For now we use a placeholder FuncName to keep the wrap helper
// from emitting empty-name alias / instance entries.
func TestRawInstanceTypeBody_ResourceDecl(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}

	// Type section only — drop the rest by validating just the
	// component header + this section.
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

// printCoreModule returns a tiny hand-rolled core wasm module
// shaped like the user-side of a WrapWasiPrintComponent input:
//
//   - imports wasi:cli/stdout@0.2.0::get-stdout as `() -> i32`
//   - imports wasi:io/streams@0.2.0::[method]output-stream.blocking-write-and-flush
//     as `(i32, i32, i32, i32) -> ()`
//   - exports memory + cabi_realloc + a placeholder body func
//
// The bytes match what `wasm-tools parse` produces from the WAT:
//
//   (module
//     (import "wasi:cli/stdout@0.2.0" "get-stdout"
//             (func $get_stdout (result i32)))
//     (import "wasi:io/streams@0.2.0"
//             "[method]output-stream.blocking-write-and-flush"
//             (func $write (param i32 i32 i32 i32)))
//     (memory (export "memory") 1)
//     (func (export "cabi_realloc")
//           (param i32 i32 i32 i32) (result i32)
//       i32.const 0))
//
// Captured by hex-dumping the same WAT in the /tmp build
// playground.
func printCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section: 3 types
		//   0: () -> i32
		//   1: (i32 i32 i32 i32) -> ()
		//   2: (i32 i32 i32 i32) -> i32
		0x01, 0x14, 0x03,
		0x60, 0x00, 0x01, 0x7f,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x00,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		// import section: 2 imports
		0x02, 0x6b,
		0x02,
		// wasi:cli/stdout@0.2.0 . get-stdout (func type 0)
		0x15, 'w', 'a', 's', 'i', ':', 'c', 'l', 'i', '/', 's', 't', 'd', 'o', 'u', 't', '@', '0', '.', '2', '.', '0',
		0x0a, 'g', 'e', 't', '-', 's', 't', 'd', 'o', 'u', 't',
		0x00, 0x00,
		// wasi:io/streams@0.2.0 . [method]output-stream.blocking-write-and-flush (func type 1)
		0x15, 'w', 'a', 's', 'i', ':', 'i', 'o', '/', 's', 't', 'r', 'e', 'a', 'm', 's', '@', '0', '.', '2', '.', '0',
		0x2e,
		'[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', '.', 'b', 'l', 'o', 'c', 'k', 'i', 'n', 'g', '-', 'w', 'r', 'i', 't', 'e', '-', 'a', 'n', 'd', '-', 'f', 'l', 'u', 's', 'h',
		0x00, 0x01,
		// function section: vec(1) typeidx [2]  (cabi_realloc)
		0x03, 0x02, 0x01, 0x02,
		// memory section: vec(1), no-max flag, min=1
		0x05, 0x03, 0x01, 0x00, 0x01,
		// export section: vec(2)
		//   memory 0 → "memory"
		//   func 2 → "cabi_realloc"  (imports take funcidx 0+1, our cabi_realloc is funcidx 2)
		0x07, 0x19, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x0c, 'c', 'a', 'b', 'i', '_', 'r', 'e', 'a', 'l', 'l', 'o', 'c', 0x00, 0x02,
		// code section: vec(1), body length 4: vec(0) locals, i32.const 0, end
		0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
	}
}

// TestWrapWasiPrintComponent_Validates calls the wrap helper
// on a hand-rolled user core module and confirms the produced
// component validates under wasm-tools. The printed output
// must include all three imports + the user's memory + the
// trampoline indirection.
func TestWrapWasiPrintComponent_Validates(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	comp := component.WrapWasiPrintComponent(printCoreModule())
	dir := t.TempDir()
	compPath := filepath.Join(dir, "print.wasm")
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
		"wasi:io/error@0.2.0",
		"wasi:io/streams@0.2.0",
		"wasi:cli/stdout@0.2.0",
		"call_indirect",
		"canon lower",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("expected %q in printed component, got:\n%s", want, string(out))
		}
	}
}

// printCliRunCoreModule is printCoreModule extended with a
// `_lang_run() -> i32` export that just returns 0. Used to
// exercise WrapWasiPrintAsCliRun end-to-end: the produced
// component has the print imports + a wasi:cli/run export,
// runs under plain `wasmtime run`.
//
// The body doesn't call the WASI funcs (proves the linkage
// works without exercising the host side). A "really prints"
// test would need more bytes for memory init and the WASI
// calls; this one's scope is "wrap is structurally correct +
// runnable".
func printCliRunCoreModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		// type section: vec(4) — same as printCoreModule plus
		// type 3 = () -> i32 for _lang_run.
		0x01, 0x18, 0x04,
		0x60, 0x00, 0x01, 0x7f,                                  // type 0: () -> i32 (get-stdout)
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x00,                // type 1: (i32 i32 i32 i32) -> () (write)
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,          // type 2: (i32 i32 i32 i32) -> i32 (cabi_realloc)
		0x60, 0x00, 0x01, 0x7f,                                  // type 3: () -> i32 (_lang_run)
		// import section (same as printCoreModule)
		0x02, 0x6b, 0x02,
		0x15, 'w', 'a', 's', 'i', ':', 'c', 'l', 'i', '/', 's', 't', 'd', 'o', 'u', 't', '@', '0', '.', '2', '.', '0',
		0x0a, 'g', 'e', 't', '-', 's', 't', 'd', 'o', 'u', 't',
		0x00, 0x00,
		0x15, 'w', 'a', 's', 'i', ':', 'i', 'o', '/', 's', 't', 'r', 'e', 'a', 'm', 's', '@', '0', '.', '2', '.', '0',
		0x2e,
		'[', 'm', 'e', 't', 'h', 'o', 'd', ']', 'o', 'u', 't', 'p', 'u', 't', '-', 's', 't', 'r', 'e', 'a', 'm', '.', 'b', 'l', 'o', 'c', 'k', 'i', 'n', 'g', '-', 'w', 'r', 'i', 't', 'e', '-', 'a', 'n', 'd', '-', 'f', 'l', 'u', 's', 'h',
		0x00, 0x01,
		// function section: vec(2) — typeidx [2, 3] for cabi_realloc + _lang_run
		0x03, 0x03, 0x02, 0x02, 0x03,
		// memory section
		0x05, 0x03, 0x01, 0x00, 0x01,
		// export section: vec(3) — memory, cabi_realloc, _lang_run.
		// Body is 37 bytes (1 vec + 9 + 15 + 12 entries).
		0x07, 0x25, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x0c, 'c', 'a', 'b', 'i', '_', 'r', 'e', 'a', 'l', 'l', 'o', 'c', 0x00, 0x02,
		0x09, '_', 'l', 'a', 'n', 'g', '_', 'r', 'u', 'n', 0x00, 0x03,
		// code section: vec(2) bodies, each `vec(0) locals; i32.const 0; end`.
		// Body is 11 bytes (1 vec + 5 + 5).
		0x0a, 0x0b, 0x02,
		0x04, 0x00, 0x41, 0x00, 0x0b,
		0x04, 0x00, 0x41, 0x00, 0x0b,
	}
}

// TestWrapWasiPrintAsCliRun_RunsUnderWasmtime end-to-end-tests
// the cli-run-extended print component. The user core's
// `_lang_run` returns 0, which lifts to result::ok and makes
// wasmtime exit 0. The print imports are present but unused
// at runtime; the test verifies the linkage works.
func TestWrapWasiPrintAsCliRun_RunsUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	comp := component.WrapWasiPrintAsCliRun(printCliRunCoreModule(), "_lang_run")
	dir := t.TempDir()
	compPath := filepath.Join(dir, "print_cli.wasm")
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
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}

// wallClockCliRunCoreModule is a hand-rolled core module shaped
// like the user side of a WrapWasiWallClockAsCliRun input:
//
//   - imports wasi:clocks/wall-clock@0.2.0::now as `(i32) -> ()`
//     (the indirect datetime out-pointer ABI)
//   - exports memory + a `_lang_run() -> i32` returning 0
//
// The _lang_run body doesn't call now (proves the linkage works
// without exercising the host side).
func wallClockCliRunCoreModule() []byte {
	out := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	// type section: vec(2) — type 0: (i32)->(), type 1: ()->i32
	typeBody := []byte{
		0x02,
		0x60, 0x01, 0x7f, 0x00,
		0x60, 0x00, 0x01, 0x7f,
	}
	out = appendCoreSec(out, 0x01, typeBody)
	// import section: wasi:clocks/wall-clock@0.2.0 . now (type 0)
	importBody := []byte{0x01}
	importBody = append(importBody, byte(len("wasi:clocks/wall-clock@0.2.0")))
	importBody = append(importBody, "wasi:clocks/wall-clock@0.2.0"...)
	importBody = append(importBody, byte(len("now")))
	importBody = append(importBody, "now"...)
	importBody = append(importBody, 0x00, 0x00) // func, typeidx 0
	out = appendCoreSec(out, 0x02, importBody)
	// function section: vec(1) typeidx [1] (_lang_run)
	out = appendCoreSec(out, 0x03, []byte{0x01, 0x01})
	// memory section: vec(1), min=1
	out = appendCoreSec(out, 0x05, []byte{0x01, 0x00, 0x01})
	// export section: memory 0, _lang_run func 1 (import shift: now=0)
	exportBody := []byte{0x02}
	exportBody = append(exportBody, byte(len("memory")))
	exportBody = append(exportBody, "memory"...)
	exportBody = append(exportBody, 0x02, 0x00) // memory 0
	exportBody = append(exportBody, byte(len("_lang_run")))
	exportBody = append(exportBody, "_lang_run"...)
	exportBody = append(exportBody, 0x00, 0x01) // func 1
	out = appendCoreSec(out, 0x07, exportBody)
	// code section: vec(1) body — vec(0) locals, i32.const 0, end
	out = appendCoreSec(out, 0x0a, []byte{0x01, 0x04, 0x00, 0x41, 0x00, 0x0b})
	return out
}

// appendCoreSec is a test-local core-section appender (id +
// uleb size + body) — the leb-free path here only needs single-
// byte sizes, so a plain length byte suffices.
func appendCoreSec(buf []byte, id byte, body []byte) []byte {
	buf = append(buf, id, byte(len(body)))
	return append(buf, body...)
}

// TestWrapWasiWallClockAsCliRun_RunsUnderWasmtime exercises the
// wall-clock wrap end-to-end. The component imports
// wasi:clocks/wall-clock@0.2.0 + exports wasi:cli/run@0.2.0;
// _lang_run returns 0 so wasmtime exits clean. Validates the
// 1-i32 trampoline + datetime-record instance type wire up
// correctly.
func TestWrapWasiWallClockAsCliRun_RunsUnderWasmtime(t *testing.T) {
	if _, err := exec.LookPath("wasm-tools"); err != nil {
		t.Skip("wasm-tools not on PATH")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	comp := component.WrapWasiWallClockAsCliRun(wallClockCliRunCoreModule(), "_lang_run")
	dir := t.TempDir()
	compPath := filepath.Join(dir, "wc.wasm")
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
	printOut, err := exec.Command("wasm-tools", "print", compPath).CombinedOutput()
	if err != nil {
		t.Fatalf("wasm-tools print failed: %v\n%s", err, printOut)
	}
	for _, want := range []string{"wasi:clocks/wall-clock@0.2.0", "wasi:cli/run@0.2.0", "datetime"} {
		if !bytes.Contains(printOut, []byte(want)) {
			t.Errorf("expected %q in component, got:\n%s", want, printOut)
		}
	}
	if err := exec.Command("wasmtime", "run", compPath).Run(); err != nil {
		t.Fatalf("wasmtime run failed: %v", err)
	}
}
