package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWatParse exercises the WAT S-expr parser (examples/self_host/
// wat_parse.fern) — slice 3 of the self-hosted binary wasm backend, which
// turns the wat_lex token stream into a tree the encoder will walk.
//
// wat_parse.fern depends on WatTok (from wat_lex.fern) but is otherwise
// import-free, so the test concatenates wat_lex.fern + wat_parse.fern + a
// self-test main() that parses a small nested form and asserts the tree
// shape, then runs it through the self-host wasm pipeline (wasm_run -> WAT
// -> wasmtime). Exit 0 = all checks pass; a failing check returns its
// 1-based id.
func TestSelfHostWatParse(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wat_parse e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	watbin, err := os.ReadFile("../../examples/self_host/watbin.fern")
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

// watParseSelfTestMain parses `(module (func $f))` and asserts the tree:
// a 2-child root list [atom "module", list [atom "func", atom "$f"]] —
// exercising nested list nodes + two-level child access. Each `return N`
// is a distinct failing-check id (0 = pass).
const watParseSelfTestMain = `
function main(): i32 {
    var root: SExpr = wat_parse(wat_tokenize("(module (func $f))"));
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
