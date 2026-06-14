package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Associated types: a trait declares `type Item;`, an impl binds it
// (`type Item = i32;`), and `Self::Item` / `I::Item` projections resolve
// to the binding — both for a direct method call and through a bounded
// generic that monomorphises. See docs/ASSOCIATED-TYPES.md.
const assocTypesSrc = `trait Iterator {
    type Item;
    function next(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Iterator for IntBox {
    type Item = i32;
    function next(self: Self): Self::Item { return self.v; }
}
function first[I: Iterator](it: I): I::Item { return it.next(); }
function main(): i32 {
    var b: IntBox = IntBox { v: 9 };
    return b.next() + first(b);   // 9 + 9 = 18
}
`

// Concrete-only variant (no bounded generic) for the x86-64 helper, which
// doesn't run the monomorph pass. Exercises `Self::Item` resolution.
const assocTypesConcreteSrc = `trait Iterator {
    type Item;
    function next(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Iterator for IntBox {
    type Item = i32;
    function next(self: Self): Self::Item { return self.v; }
}
function main(): i32 {
    var b: IntBox = IntBox { v: 9 };
    return b.next();   // 9
}
`

func TestInterpAssociatedTypes(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(assocTypesSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 18 {
		t.Errorf("exit = %d, want 18\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64AssociatedTypes(t *testing.T) {
	out, code := compileAndRunX86_64(t, assocTypesConcreteSrc)
	if code != 9 {
		t.Errorf("exit = %d, want 9\n%s", code, out)
	}
}

func TestArm64AssociatedTypes(t *testing.T) {
	out, code := compileAndRunArm64(t, assocTypesSrc)
	if code != 18 {
		t.Errorf("exit = %d, want 18\n%s", code, out)
	}
}

func TestWASMAssociatedTypes(t *testing.T) {
	if code := runWasm(t, assocTypesSrc); code != 18 {
		t.Errorf("wasm exit = %d, want 18", code)
	}
}
