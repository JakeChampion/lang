package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostIRExportBridge(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	const exporter = `@export("local:test/nums@0.1.0", "iota")
function iota(): i32[] { return [10, 20, 30, 40]; }

function main(): i32 { return 0; }
`
	out, stderr, code := runDriver(t, runner, driverBin, []byte(exporter), false)
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
	if !strings.Contains(wat, "$__xwrap_iota") {
		t.Error("the @export is not routed through its canonical-ABI wrapper")
	}
}

func TestSelfHostIRExportBridgeInert(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "wasm_ir_run")

	const plain = "struct P { x: i32, y: i32 }\n" +
		"function main(): i32 { let p = P { x: 40, y: 2 }; let a = [p.x, p.y]; let s = \"hi\"; return a[0] + a[1] + s.len(); }\n"
	pout, pstderr, pcode := runDriver(t, runner, driverBin, []byte(plain), false)
	if pcode != 0 {
		t.Fatalf("IR emit failed (exit %d): %s", pcode, pstderr)
	}
	wat := string(pout)
	if len(wat) == 0 {
		t.Fatal("IR path emitted 0 bytes")
	}
	for _, unwanted := range []string{"__xwrap_", "__import", "(export \"local:"} {
		if strings.Contains(wat, unwanted) {
			t.Errorf("extern/export surface leaked into an extern-free module: found %q", unwanted)
		}
	}
}
