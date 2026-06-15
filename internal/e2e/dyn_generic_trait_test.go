package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Generic dyn-trait objects (#2691 trait-spine): `dyn Container[i32]` is a
// runtime trait object over a *pinned* instantiation of a generic trait.
// The trait argument is erased at runtime (the vtable is keyed by trait
// name); it drives only the checker's coercion gate and the
// method-signature substitution that makes `get(): T` read as `get(): i32`.
// Two concrete types implement Container[i32]; both box into the same
// `dyn Container[i32]` and dispatch by concrete type. BoxI.get() = 40,
// Pair.get() = 1 + 1 = 2; 40 + 2 = 42.
const dynGenericTraitSrc = `trait Container[T] {
    function get(self: Self): T;
}
struct BoxI { v: i32 }
impl Container[i32] for BoxI {
    function get(self: Self): i32 { return self.v; }
}
struct Pair { a: i32, b: i32 }
impl Container[i32] for Pair {
    function get(self: Self): i32 { return self.a + self.b; }
}
function sum(d: dyn Container[i32]): i32 {
    return d.get();
}
function main(): i32 {
    var x: dyn Container[i32] = BoxI { v: 40 };
    var y: dyn Container[i32] = Pair { a: 1, b: 1 };
    return sum(x) + sum(y);
}
`

func TestInterpDynGenericTrait(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(dynGenericTraitSrc), 0o644); err != nil {
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

func TestX86_64DynGenericTrait(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynGenericTraitSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64DynGenericTrait(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynGenericTraitSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMDynGenericTrait(t *testing.T) {
	if code := runWasm(t, dynGenericTraitSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
