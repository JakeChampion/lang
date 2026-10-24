package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostCrossUnitFnValue(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	files := map[string]string{
		"apply.fern": `import "std/http";
pub function apply_i32(f: (i32) => i32, n: i32): i32 { return f(n); }
`,
		"main.fern": `import "std/http";
import "./apply";

function dbl(n: i32): i32 { return n * 2; }

function main(): i32 {
    return apply.apply_i32(dbl, 21);
}
`,
	}
	progDir := t.TempDir()
	bsrc, err := os.ReadFile("../../../compiler/builtins.fern")
	if err != nil {
		t.Fatalf("read builtins.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "builtins.fern"), bsrc, 0o644); err != nil {
		t.Fatalf("write builtins.fern: %v", err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(progDir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	asm := string(runDriverFile(t, runner, driverBin, filepath.Join(progDir, "main.fern")))
	if !strings.Contains(asm, ".Lssa_") {
		t.Fatal("program did not route through the per-module IR path")
	}
	bin := buildBin(t, gcc, progDir, "cross_unit_fnvalue", asm)
	if _, exit := runBin(binCmd(runner, bin), ""); exit != 42 {
		t.Errorf("exit = %d, want 42 (dbl(21) through a cross-unit fn value)", exit)
	}
}
