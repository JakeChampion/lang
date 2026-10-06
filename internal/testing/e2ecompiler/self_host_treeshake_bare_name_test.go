package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTreeshakeBareNameKeepsFreeFunctionOnly: a bare identifier keeps
// the free function of that name and not a method sharing it. The method
// here calls subprocess, which wasm32-wasi does not provide, so keeping it
// by name fails E066 on a program that never reaches it — what every
// wasi:http handler hit through std/async's `.handle()` method.
func TestSelfHostTreeshakeBareNameKeepsFreeFunctionOnly(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	dir := t.TempDir()
	prog := filepath.Join(dir, "main.fern")
	src := `struct D { n: i32 }
function (d: D) run(): ProcessResult { return subprocess("true", [], ""); }
function run(): i32 { return 1; }
function main(): i32 { return run(); }
`
	if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "-check", "-target", "wasm32-wasi", prog, stdlib).CombinedOutput()
	if err != nil || strings.Contains(string(out), "E066") {
		t.Fatalf("the unreachable method's subprocess was counted against the target: %v\n%s", err, out)
	}
}
