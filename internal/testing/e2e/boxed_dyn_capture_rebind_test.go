package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const boxedDynCaptureRebindSrc = `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Ci { r: i32 }
impl Shape for Ci { function area(self: Self): i32 { return self.r * 3i32; } }

function main(): i32 {
    let d: dyn Shape = Sq { s: 3i32 };
    let f: () => i32 = (() => d.area());
    d = Ci { r: 5i32 };
    return f();
}
`

func TestInterpBoxedDynCaptureRebind(t *testing.T) {
	bin := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(src, []byte(boxedDynCaptureRebindSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 15 {
		t.Errorf("exit = %d, want 15\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64BoxedDynCaptureRebind(t *testing.T) {
	out, code := compileAndRunX86_64(t, boxedDynCaptureRebindSrc)
	if code != 15 {
		t.Errorf("exit = %d, want 15\n%s", code, out)
	}
}

func TestArm64BoxedDynCaptureRebind(t *testing.T) {
	out, code := compileAndRunArm64(t, boxedDynCaptureRebindSrc)
	if code != 15 {
		t.Errorf("exit = %d, want 15\n%s", code, out)
	}
}

func TestWASMBoxedDynCaptureRebind(t *testing.T) {
	if code := runWasm(t, boxedDynCaptureRebindSrc); code != 15 {
		t.Errorf("wasm exit = %d, want 15", code)
	}
}
