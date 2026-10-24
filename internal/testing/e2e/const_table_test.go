package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const constTableSrc = `import "std/i32";

const T: u8[] = [0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8, 1u8, 2u8, 0u8];

function scan(s: string): i32 {
  return __scan_set(s, 0, T);
}

function at(i: i32): i32 {
  return T[i] as i32;
}

function main(): i32 {
  print(scan("ccb").to_string() + " " + at(200).to_string() + " " + T.len().to_string());
  return 0;
}
`

const constTableWant = "2 2 256\n"

func TestConstTableWasmEmitsNoElementStores(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(srcPath, []byte(constTableSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	wat := filepath.Join(dir, "prog.wat")
	if out, err := exec.Command(buildLangBinForInterp(t), "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, srcPath).CombinedOutput(); err != nil {
		t.Fatalf("wasm emit: %v\n%s", err, out)
	}
	text, err := os.ReadFile(wat)
	if err != nil {
		t.Fatal(err)
	}
	if n := watFuncLines(string(text), "$main"); n > 400 {
		t.Errorf("$main is %d WAT lines, want at most 400: the table's elements are being materialised", n)
	}
}

func watFuncLines(wat, fn string) int {
	head := regexp.MustCompile(`^\s*\(func (\$\S+)`)
	n, in := 0, false
	for _, line := range strings.Split(wat, "\n") {
		if m := head.FindStringSubmatch(line); m != nil {
			in = m[1] == fn
			continue
		}
		if in && strings.HasPrefix(line, "    ") {
			n++
		}
	}
	return n
}

func TestInterpConstTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(path, []byte(constTableSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
	if code != 0 || out != constTableWant {
		t.Fatalf("exit %d, printed %q, want %q\nstderr: %s", code, out, constTableWant, errOut)
	}
}

func TestX86_64ConstTable(t *testing.T) {
	out, code := compileAndRunX86_64(t, constTableSrc)
	if code != 0 || out != constTableWant {
		t.Fatalf("exit %d, printed %q, want %q", code, out, constTableWant)
	}
}

func TestWasmConstTable(t *testing.T) {
	if out := compileAndRunWasmCapture(t, constTableSrc); out != constTableWant {
		t.Fatalf("printed %q, want %q", out, constTableWant)
	}
}
