package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostWatParse(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wat_parse e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	watbin, err := os.ReadFile("../../../compiler/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(watbin) + "\n" + watParseSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wat_parse self-test")
	}
	watPath := filepath.Join(dir, "wat_parse_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wat_parse self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const watParseSelfTestMain = `
function main(): i32 {
    let root: SExpr = wat_parse(wat_tokenize("(module (func $f))"));
    if (root.kind != 0) { return 1; }
    if (root.items.len() != 2) { return 2; }
    if (root.items[0].kind != 2 || root.items[0].text != "module") { return 3; }
    if (root.items[1].kind != 0) { return 4; }
    if (root.items[1].items.len() != 2) { return 5; }
    if (root.items[1].items[0].text != "func") { return 6; }
    if (root.items[1].items[1].text != "$f") { return 7; }
    return 0;
}
`
