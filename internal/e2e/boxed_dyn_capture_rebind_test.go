package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A `dyn Trait` local a closure captures and the enclosing scope rebinds
// lives in a boxcapture cell, and the rebind is an element store into that
// cell. On wasm a `dyn` is an inline two-word `[data, vtable]`, and the
// index-assign path chose its store width by hand and knew nothing of that,
// so the store wrote one word and left the other on the operand stack: the
// closure kept reading the Sq and answered 9, and the CLI's module failed
// validation outright (#8797's program). Every engine answers the Ci's 15.
const boxedDynCaptureRebindSrc = `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Ci { r: i32 }
impl Shape for Ci { function area(self: Self): i32 { return self.r * 3i32; } }

function main(): i32 {
    var d: dyn Shape = Sq { s: 3i32 };
    var f: () => i32 = (() => d.area());
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
