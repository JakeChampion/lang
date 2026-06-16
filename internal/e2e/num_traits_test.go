package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/num arithmetic operator traits + the Num supertrait (#2706, and the
// foundation for #2663's generic numeric methods): a `T: num.Num` bound lets a
// generic function dispatch arithmetic by method (`s.add(...)`) for ANY
// implementing element type — here the SAME generic `total` runs over both an
// i32[] and an i64[]. 10+20 = 30, 9+3 = 12, 30 + 12 = 42.
const numTraitsSrc = `import "std/num" as num;
function (a: T[]) total[T: num.Num](init: T): T {
    var s = init;
    var i = 0;
    while (i < a.len()) { s = s.add(a[i]); i = i + 1; }
    return s;
}
function main(): i32 {
    var i32s: i32[] = [10, 20];
    var i64s: i64[] = [9, 3];
    return i32s.total(0) + (i64s.total(0) as i32);
}
`

func TestInterpNumTraits(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(numTraitsSrc), 0o644); err != nil {
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

func TestArm64NumTraits(t *testing.T) {
	if out, code := compileAndRunArm64(t, numTraitsSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMNumTraits(t *testing.T) {
	if code := runWasm(t, numTraitsSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
