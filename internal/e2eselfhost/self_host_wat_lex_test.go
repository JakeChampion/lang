package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWatLex exercises the WAT tokenizer (examples/self_host/
// wat_lex.fern) — slice 2 of the self-hosted binary wasm backend, which
// assembles the folded-S-expr WAT that the wasm emitter emits rather than
// re-deriving lowering from the AST.
//
// Like leb128.fern, wat_lex.fern is import-free, so the test reads it from
// disk and concatenates it with a self-test main() that tokenizes a small
// WAT snippet (with a `(data "…")` string + escape) and asserts the token
// kinds / texts, then runs the combined program through the self-host wasm
// pipeline (wasm_run -> WAT -> wasmtime). Exit 0 = all checks pass; a
// failing check returns its 1-based id.
func TestSelfHostWatLex(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wat_lex e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	lex, err := os.ReadFile("../../examples/self_host/watbin.fern")
	if err != nil {
		t.Fatalf("read watbin.fern: %v", err)
	}
	source := string(lex) + "\n" + watLexSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the wat_lex self-test")
	}
	watPath := filepath.Join(dir, "wat_lex_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wat_lex self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

// watLexSelfTestMain tokenizes a nested form and a `(data "\0a")` string,
// asserting paren/atom/string kinds and texts (kind 0=( 1=) 2=atom
// 3=string). Each `return N` is a distinct failing-check id (0 = pass).
const watLexSelfTestMain = `
function main(): i32 {
    var ts: WatTok[] = wat_tokenize("(module (func $f (result i32)))");
    if (ts.len() != 11) { return 1; }
    if (ts[0].kind != 0) { return 2; }
    if (ts[1].kind != 2 || ts[1].text != "module") { return 3; }
    if (ts[3].text != "func") { return 4; }
    if (ts[4].text != "$f") { return 5; }
    if (ts[7].text != "i32") { return 6; }
    if (ts[10].kind != 1) { return 7; }
    var ds: WatTok[] = wat_tokenize("(data \"\\0a\")");
    if (ds.len() != 4) { return 8; }
    if (ds[1].text != "data") { return 9; }
    if (ds[2].kind != 3 || ds[2].text != "\\0a") { return 10; }
    return 0;
}
`
