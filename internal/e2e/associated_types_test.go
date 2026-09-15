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

// A PARAMETRIC impl binding an associated type to its OWN type parameter
// (`impl[T] Carrier for Box[T] { type Ok = T; }`). Every case above binds a
// concrete type on a non-generic impl, which is why this shape shipped broken:
// the binding stayed a same-named StructType rather than the impl's ParamType,
// and nothing substituted the base's type arguments into it.
//
// Exercises both resolution paths in one program — the direct call (`b.get()`,
// resolved in the first check pass) and the bounded generic (`unwrap(b)`, whose
// `C::Ok` only becomes concrete at monomorph, and whose projection base has to
// be mangled alongside the struct it names).
const assocTypesGenericImplSrc = `trait Carrier {
    type Ok;
    function get(self: Self): Self::Ok;
}
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function unwrap[C: Carrier](c: C): C::Ok { return c.get(); }
function main(): i32 {
    var b: Box[i32] = Box { v: 20 };
    return b.get() + unwrap(b);   // 20 + 20 = 40
}
`

func TestInterpAssociatedTypesGenericImpl(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(assocTypesGenericImplSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 40 {
		t.Errorf("exit = %d, want 40\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64AssociatedTypesGenericImpl(t *testing.T) {
	out, code := compileAndRunX86_64(t, assocTypesGenericImplSrc)
	if code != 40 {
		t.Errorf("exit = %d, want 40\n%s", code, out)
	}
}

func TestArm64AssociatedTypesGenericImpl(t *testing.T) {
	out, code := compileAndRunArm64(t, assocTypesGenericImplSrc)
	if code != 40 {
		t.Errorf("exit = %d, want 40\n%s", code, out)
	}
}

func TestWASMAssociatedTypesGenericImpl(t *testing.T) {
	if code := runWasm(t, assocTypesGenericImplSrc); code != 40 {
		t.Errorf("wasm exit = %d, want 40", code)
	}
}

// A projection in a PARAMETER — `pick[H: Holder](h: H, d: H::Item)` — resolves
// once an earlier argument pins its base. Projections in the RESULT have always
// resolved (the generic-call path resolves after substitution); parameters had
// no equivalent, so every value handed to one was compared against an
// unresolved `H::Item` and rejected.
//
// Covers both bases in one program: a generic STRUCT, which monomorph flattens
// to `Box__i32` with a synthesised concrete impl, and a generic ENUM, which it
// deliberately leaves generic — so the enum's projection has to be resolved
// while monomorph substitutes, there being no instantiation left to look it up
// against afterwards.
//
// The direct `e.get(1)` is required by #9308 (a parametric impl on a generic
// enum is unreachable through a bound unless the method is also called
// directly), not by anything this test is about.
const assocTypesProjInParamSrc = `trait Holder {
    type Item;
    function get(self: Self, d: Self::Item): Self::Item;
}
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = T;
    function get(self: Self, d: Self::Item): Self::Item { return self.v; }
}
enum E[T] { A(T), B }
impl[T] Holder for E[T] {
    type Item = T;
    function get(self: Self, d: Self::Item): Self::Item {
        match (self) { A(x) => { return x; }, B => { return d; } }
    }
}
function pick[H: Holder](h: H, d: H::Item): H::Item { return h.get(d); }
function main(): i32 {
    var b: Box[i32] = Box { v: 20 };
    var e: E[i32] = A(15);
    return pick(b, 0) + pick(e, 0) + e.get(1);   // 20 + 15 + 15 = 50
}
`

func TestInterpAssociatedTypesProjectionInParameter(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(assocTypesProjInParamSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 50 {
		t.Errorf("exit = %d, want 50\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}

func TestX86_64AssociatedTypesProjectionInParameter(t *testing.T) {
	out, code := compileAndRunX86_64(t, assocTypesProjInParamSrc)
	if code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestArm64AssociatedTypesProjectionInParameter(t *testing.T) {
	out, code := compileAndRunArm64(t, assocTypesProjInParamSrc)
	if code != 50 {
		t.Errorf("exit = %d, want 50\n%s", code, out)
	}
}

func TestWASMAssociatedTypesProjectionInParameter(t *testing.T) {
	if code := runWasm(t, assocTypesProjInParamSrc); code != 50 {
		t.Errorf("wasm exit = %d, want 50", code)
	}
}
