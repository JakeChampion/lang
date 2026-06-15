package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Generic traits (`trait Container[T]` / `trait From[T]`): a trait may take
// type parameters, each impl binds them (`impl Container[i32] for B`), and
// the conformance check substitutes them so the concrete method dispatches.
// Container[i32].get → 7 (×10) + From[i32].from(20).deg → 20 = 90. See
// docs/TRAITS.md.
const genericTraitSrc = `trait Container[T] { function get(self: Self): T; }
struct IntBox { v: i32 }
impl Container[i32] for IntBox { function get(self: Self): i32 { return self.v; } }
trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }
function main(): i32 {
    var b: IntBox = IntBox { v: 7 };
    var c: Celsius = Celsius.from(20);
    return b.get() * 10 + c.deg;
}
`

func TestInterpGenericTrait(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(genericTraitSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 90 {
		t.Errorf("exit = %d, want 90\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64GenericTrait(t *testing.T) {
	if out, code := compileAndRunX86_64(t, genericTraitSrc); code != 90 {
		t.Errorf("exit = %d, want 90\n%s", code, out)
	}
}

func TestArm64GenericTrait(t *testing.T) {
	if out, code := compileAndRunArm64(t, genericTraitSrc); code != 90 {
		t.Errorf("exit = %d, want 90\n%s", code, out)
	}
}

func TestWASMGenericTrait(t *testing.T) {
	if code := runWasm(t, genericTraitSrc); code != 90 {
		t.Errorf("wasm exit = %d, want 90", code)
	}
}
