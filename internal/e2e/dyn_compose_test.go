package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Composition of the dyn-trait features (#2691 spine: generic dyn objects
// #3284, downcast #3285, associated-type pinning #3346) at their combinatorial
// corners — the most regression-prone surface, since they share the
// DynTraitType canonicalisation (sorted/deduped (trait, args, assoc) triples)
// and the per-trait signature resolution.
//
//  1. `as?` downcast THROUGH an associated-type-pinned object recovers the
//     concrete type (the runtime tag is the concrete type; the pin is erased).
//     B.v = 41, +1 = 42.
//  2. A multi-trait object mixing a GENERIC-arg trait and an ASSOC-pinned trait
//     (`dyn Get[i32] + P[Item = i32]`): the concrete impls both, method
//     resolution spans the union, and `get(): T` / `pi(): Self::Item` both read
//     as i32. 41 + 1 = 42.
const dynComposeDowncastSrc = `trait P { type Item; function get(self: Self): Self::Item; }
struct B { v: i32 }
impl P for B { type Item = i32; function get(self: Self): i32 { return self.v; } }
function main(): i32 {
    var d: dyn P[Item = i32] = B { v: 41 };
    match (d as? B) {
        Some(b) => { return b.v + 1; },
        None => { return 0; }
    }
}
`

const dynComposeMultiSrc = `trait Get[T] { function get(self: Self): T; }
trait P { type Item; function pi(self: Self): Self::Item; }
struct W { v: i32 }
impl Get[i32] for W { function get(self: Self): i32 { return self.v; } }
impl P for W { type Item = i32; function pi(self: Self): i32 { return 1; } }
function sum(d: dyn Get[i32] + P[Item = i32]): i32 { return d.get() + d.pi(); }
function main(): i32 { return sum(W { v: 41 }); }
`

func runInterpExit(t *testing.T, src string) int {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("interp did not run\nstderr: %s", errb.String())
	}
	return cmd.ProcessState.ExitCode()
}

func TestInterpDynComposeDowncast(t *testing.T) {
	if code := runInterpExit(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
func TestX86_64DynComposeDowncast(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestArm64DynComposeDowncast(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestWASMDynComposeDowncast(t *testing.T) {
	if code := runWasm(t, dynComposeDowncastSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}

func TestInterpDynComposeMulti(t *testing.T) {
	if code := runInterpExit(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42", code)
	}
}
func TestX86_64DynComposeMulti(t *testing.T) {
	if out, code := compileAndRunX86_64(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestArm64DynComposeMulti(t *testing.T) {
	if out, code := compileAndRunArm64(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("exit = %d, want 42\n%s", code, out)
	}
}
func TestWASMDynComposeMulti(t *testing.T) {
	if code := runWasm(t, dynComposeMultiSrc); code != 42 {
		t.Errorf("wasm exit = %d, want 42", code)
	}
}
