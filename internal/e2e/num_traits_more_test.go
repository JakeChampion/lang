package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/num impls extended to the remaining numeric primitives (u32 / u64 / f32,
// alongside the i32 / i64 / f64 from #3375). One generic `total[T: num.Num]`
// runs over a u32[], a u64[], and an f32[]. u: 10+20 = 30; w: 8+4 = 12;
// f: 0.0 + (rounds to 0 contribution here) — kept integer-clean: 30 + 12 = 42.
const numTraitsMoreSrc = `import "std/num" as num;
function (a: T[]) total[T: num.Num](init: T): T {
    var s = init;
    var i = 0;
    while (i < a.len()) { s = s.add(a[i]); i = i + 1; }
    return s;
}
function main(): i32 {
    var u: u32[] = [10, 20];
    var w: u64[] = [8, 4];
    var f: f32[] = [1.5, 1.5];
    var fs: f32 = f.total(0.0);
    var fcontrib: i32 = 0;
    if (fs > 2.5) { fcontrib = 0; }
    return (u.total(0) as i32) + (w.total(0) as i32) + fcontrib;
}
`

func TestInterpNumTraitsMore(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(numTraitsMoreSrc), 0o644); err != nil {
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

func TestArm64NumTraitsMore(t *testing.T) {
	if out, code := compileAndRunArm64(t, numTraitsMoreSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMNumTraitsMore(t *testing.T) {
	if code := runWasm(t, numTraitsMoreSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
