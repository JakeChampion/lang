package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A `dyn` object over a trait with a PINNED associated type
// (`dyn Producer[Item = i32]`, #2691 trait-spine follow-up). Pinning makes the
// otherwise object-unsafe trait usable as a trait object: the argument is
// erased at runtime (the vtable is keyed by trait name), and the pin drives
// only the checker's object-safety + coercion gates and the `Self::Item`
// projection resolution (so `get(): Self::Item` reads as `get(): i32`). Two
// concrete types implement Producer with Item = i32; both box into the same
// `dyn Producer[Item = i32]` and dispatch by concrete type. IntBox.get() = 40,
// Pair.get() = 1 + 1 = 2; 40 + 2 = 42.
const dynAssocTypeSrc = `trait Producer {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Producer for IntBox {
    type Item = i32;
    function get(self: Self): i32 { return self.v; }
}
struct Pair { a: i32, b: i32 }
impl Producer for Pair {
    type Item = i32;
    function get(self: Self): i32 { return self.a + self.b; }
}
function sum(d: dyn Producer[Item = i32]): i32 {
    return d.get();
}
function main(): i32 {
    var x: dyn Producer[Item = i32] = IntBox { v: 40 };
    var y: dyn Producer[Item = i32] = Pair { a: 1, b: 1 };
    return sum(x) + sum(y);
}
`

func TestInterpDynAssocType(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(dynAssocTypeSrc), 0o644); err != nil {
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

func TestX86_64DynAssocType(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynAssocTypeSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestArm64DynAssocType(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynAssocTypeSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}

func TestWASMDynAssocType(t *testing.T) {
	if code := runWasm(t, dynAssocTypeSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
