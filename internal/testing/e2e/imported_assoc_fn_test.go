package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const importedAssocFnZeroSrc = `import "std/num" as num;
function (a: T[]) total[T: num.Num + num.Zero](): T {
    let s: T = T.zero();
    let i: i32 = 0;
    while (i < a.len()) { s = s.add(a[i]); i = i + 1; }
    return s;
}
function main(): i32 {
    let xs: i32[] = [40, 2];
    return xs.total();
}
`

func TestInterpImportedAssocFnZero(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(importedAssocFnZeroSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("exit = %d, want 42\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestArm64ImportedAssocFnZero(t *testing.T) {
	if out, code := compileAndRunArm64(t, importedAssocFnZeroSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMImportedAssocFnZero(t *testing.T) {
	if code := runWasm(t, importedAssocFnZeroSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
