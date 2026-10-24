package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const genericTraitBoundSrc = `trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }
function describe[T: From[i32]](proto: T, v: i32): T { return T.from(v); }
function main(): i32 {
    let zero: Celsius = Celsius { deg: 0 };
    let c: Celsius = describe(zero, 20);
    return c.deg;
}
`

func TestInterpGenericTraitBound(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(genericTraitBoundSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 20 {
		t.Errorf("exit = %d, want 20\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestArm64GenericTraitBound(t *testing.T) {
	if out, code := compileAndRunArm64(t, genericTraitBoundSrc); code != 20 {
		t.Errorf("exit = %d, want 20\n%s", code, out)
	}
}

func TestWASMGenericTraitBound(t *testing.T) {
	if code := runWasm(t, genericTraitBoundSrc); code != 20 {
		t.Errorf("wasm exit = %d, want 20", code)
	}
}
