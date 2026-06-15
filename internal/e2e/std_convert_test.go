package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/convert provides the canonical `From[T]` / `Into[T]` conversion
// traits (built on generic traits, #3254). Celsius.from(20).into():
// Fahrenheit = 20*9/5+32 = 68. See std/convert.fern.
const stdConvertSrc = `import "std/convert" as convert;
struct Celsius { deg: i32 }
struct Fahrenheit { deg: i32 }
impl convert.From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }
impl convert.Into[Fahrenheit] for Celsius { function into(self: Self): Fahrenheit { return Fahrenheit { deg: self.deg * 9 / 5 + 32 }; } }
function main(): i32 {
    var c: Celsius = Celsius.from(20);
    var f: Fahrenheit = c.into();
    return f.deg;
}
`

func TestInterpStdConvert(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(stdConvertSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 68 {
		t.Errorf("exit = %d, want 68\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64StdConvert(t *testing.T) {
	if out, code := compileAndRunX86_64(t, stdConvertSrc); code != 68 {
		t.Errorf("exit = %d, want 68\n%s", code, out)
	}
}

func TestArm64StdConvert(t *testing.T) {
	if out, code := compileAndRunArm64(t, stdConvertSrc); code != 68 {
		t.Errorf("exit = %d, want 68\n%s", code, out)
	}
}

func TestWASMStdConvert(t *testing.T) {
	if code := runWasm(t, stdConvertSrc); code != 68 {
		t.Errorf("wasm exit = %d, want 68", code)
	}
}
