package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `-o` names the output BINARY, and with it unset the default emitter writes
// assembly to stdout — the usage text says so. `-backend ssa` refused instead,
// which left disassembling the linked ELF as the only way to read what the SSA
// backend generated. That ELF carries neither sections nor symbols, so a reader
// could not even find a function in it: comparing the two backends' code for a
// hot loop was effectively impossible.
func TestArm64SSAWritesAsmToStdoutWithoutO(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  var i: i32 = 0;\n  while (i < 3) { i = i + 1; }\n  return i;\n}\n")

	out, err := exec.Command(bin, "-target", "arm64-linux", "-backend", "ssa", entry).Output()
	if err != nil {
		t.Fatalf("-backend ssa without -o: %v", err)
	}
	asm := string(out)
	for _, want := range []string{".text", "_start:", "fn_main"} {
		if !strings.Contains(asm, want) {
			t.Errorf("assembly missing %q; got %d bytes:\n%s", want, len(asm), firstLines(asm, 12))
		}
	}
}

// The wasm SSA backend produces a wasm BINARY, not assembly, so there is
// nothing to write to stdout and -o stays required. The message has to say
// which target it is refusing for, now that the arm64 one does not refuse.
func TestWasmSSAStillRequiresOutputPath(t *testing.T) {
	bin := buildFernForStdoutTest(t)
	entry := writeFern(t, "function main(): i32 {\n  return 0;\n}\n")

	out, err := exec.Command(bin, "-target", "wasm32-wasi", "-backend", "ssa", entry).CombinedOutput()
	if err == nil {
		t.Fatal("wasm ssa without -o should fail: it emits a binary, not text")
	}
	for _, want := range []string{"wasm32-wasi", "-o OUTPUT"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("error missing %q:\n%s", want, out)
		}
	}
}

func buildFernForStdoutTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fern")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fern: %v\n%s", err, o)
	}
	return bin
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
