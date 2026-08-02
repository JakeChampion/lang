package e2eselfhost

import (
	"strings"
	"testing"
)

// TestSelfHostIRExportBridge pins that the IR path emits the WIT extern/export
// canonical-ABI bridge — the one part of the wasm surface that had no IR sibling.
//
// It showed up two ways, both the same cause:
//
//   - component mode REFUSED any module declaring an `@import` extern or an
//     `@export` binding (component_ir_core_ok), so such modules fell through to
//     the AST emitter;
//   - mode 0 never consulted that gate, so an `@export` module routed IR and
//     emitted a core whose only exports were `memory` and `_start` — the binding
//     SILENTLY DROPPED. Measured on the pre-fix tree: 11,108 bytes, no `iota`.
//
// The bridge lives in wasm_ir with the heap-box geometry parameterised by which
// consumer will read the boxes it fills (wasm_ir.xbox_field_off — the IR path
// slots 8, the legacy AST emitter 4). Moving it WITHOUT that parameter is what
// #5974 tried: the functions are pure, but purity is not layout-independence, and
// every record / variant / tuple extern then read its leaves from the wrong
// offsets. TestSelfHostWasmExternBridgeIRLayout pins the offsets per shape.
//
// This test covers the mode-0 half, which is the half that was silently wrong and
// is reachable from a stdin driver. The component half is covered by the
// TestSelfHostExport*/TestSelfHostExtern* component tests, which compose a real
// component and run it under wasmtime.
func TestSelfHostIRExportBridge(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	const exporter = `@export("local:test/nums@0.1.0", "iota")
function iota(): i32[] { return [10, 20, 30, 40]; }

function main(): i32 { return 0; }
`
	// `-ir` forces the IR path, which is the path under test.
	out, stderr, code := runDriver(t, runner, driverBin, []byte(exporter), false, "-ir")
	if code != 0 {
		t.Fatalf("IR emit failed (exit %d): %s", code, stderr)
	}
	wat := string(out)
	if len(wat) == 0 {
		t.Fatal("IR path emitted 0 bytes for an @export module")
	}
	if !strings.Contains(wat, `(export "local:test/nums@0.1.0#iota"`) {
		t.Errorf("IR path dropped the @export binding — the extern/export bridge is not being emitted\n%s", wat)
	}
	// The export must resolve to the canonical-ABI wrapper, not the raw Fern
	// function: an i32[] result is lifted through a return area, so exporting
	// $iota directly would hand the consumer a bare pointer.
	if !strings.Contains(wat, "$__xwrap_iota") {
		t.Error("the @export is not routed through its canonical-ABI wrapper")
	}
}

// TestSelfHostIRExportBridgeInert pins the other half of the contract: a module
// declaring no extern and no export must be completely unaffected. The bridge
// emitters are gated (module_needs_extern_wrappers / an empty export list), so
// wiring them in must add nothing to an ordinary program — which is what makes
// the change safe for the ~440 wasm test files that declare neither.
func TestSelfHostIRExportBridgeInert(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "wasm_ir_run")

	const plain = "struct P { x: i32, y: i32 }\n" +
		"function main(): i32 { var p = P { x: 40, y: 2 }; var a = [p.x, p.y]; var s = \"hi\"; return a[0] + a[1] + s.len(); }\n"
	pout, pstderr, pcode := runDriver(t, runner, driverBin, []byte(plain), false, "-ir")
	if pcode != 0 {
		t.Fatalf("IR emit failed (exit %d): %s", pcode, pstderr)
	}
	wat := string(pout)
	if len(wat) == 0 {
		t.Fatal("IR path emitted 0 bytes")
	}
	// No extern surface at all: the only exports are the ones every core has.
	for _, unwanted := range []string{"__xwrap_", "__import", "(export \"local:"} {
		if strings.Contains(wat, unwanted) {
			t.Errorf("extern/export surface leaked into an extern-free module: found %q", unwanted)
		}
	}
}
