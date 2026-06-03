package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end: a `trait` + `impl Trait for Type` makes the impl methods
// callable through the ordinary method-dispatch path on the
// interpreter. This is the Phase 1 contract from docs/TRAITS.md —
// impl methods are desugared to receiver-methods, so no codegen / IR /
// interp change is needed; `p.to_string()` just resolves to
// `__method_Point_to_string`.
func TestInterpTraitImplDispatch(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Display {
    function to_string(self: Self): string;
}

struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "(" + self.x.to_string() + ", " + self.y.to_string() + ")";
    }
}

function main(): i32 {
    var p: Point = Point { x: 3, y: 7 };
    print(p.to_string());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	code := cmd.ProcessState.ExitCode()
	if code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "(3, 7)") {
		t.Errorf("stdout missing trait-dispatched output `(3, 7)`: %q (stderr: %s)", out.String(), errb.String())
	}
}

// A type may implement more than one trait, and a trait may be
// implemented for several types; each impl's methods dispatch
// independently. Exercises a built-in-type impl (i32) alongside a
// struct impl.
func TestInterpTraitMultipleImpls(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Named {
    function name(self: Self): string;
}

struct Dog { age: i32 }

impl Named for Dog {
    function name(self: Self): string { return "dog"; }
}

impl Named for i32 {
    function name(self: Self): string { return "int"; }
}

function main(): i32 {
    var d: Dog = Dog { age: 2 };
    var n: i32 = 5;
    print(d.name());
    print(n.name());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "dog") || !strings.Contains(got, "int") {
		t.Errorf("stdout missing both impl outputs (`dog`, `int`): %q (stderr: %s)", got, errb.String())
	}
}

// End-to-end: a trait-bounded generic function `show[T: Display](v: T)`
// dispatches `v.to_string()` to the right impl per instantiation. The
// monomorphiser clones `show` per concrete type and the re-check
// resolves each call to the impl's mangled method. An empty
// `impl Display for i32 {}` adopts i32's pre-existing std/i32
// to_string. See docs/TRAITS.md (Phase 2).
func TestInterpBoundedGenericDispatch(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/i32";

trait Display {
    function to_string(self: Self): string;
}

struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "Point(" + self.x.to_string() + ")";
    }
}

// Empty impl adopts i32's existing to_string from std/i32.
impl Display for i32 { }

function show[T: Display](v: T): string {
    return v.to_string();
}

function main(): i32 {
    var p: Point = Point { x: 9, y: 2 };
    print(show(p));
    print(show(42));
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "Point(9)") || !strings.Contains(got, "42") {
		t.Errorf("stdout missing per-instantiation dispatch output (`Point(9)`, `42`): %q (stderr: %s)", got, errb.String())
	}
}

// Cross-module trait coherence (Phase 3): a trait + impl declared in a
// sibling module are usable from the entry module — both for a direct
// method call and through a trait-bounded generic whose bound names the
// trait with a `mod.Trait` qualifier. modload mangles the trait name,
// the impl's `for` type, and the bound to line up. See docs/TRAITS.md.
func writeTraitProject(t *testing.T, main string) string {
	t.Helper()
	dir := t.TempDir()
	lib := `pub trait Area {
    function area(self: Self): i32;
}
pub struct Square { side: i32 }
impl Area for Square {
    function area(self: Self): i32 { return self.side * self.side; }
}
`
	if err := os.WriteFile(filepath.Join(dir, "shapes.fern"), []byte(lib), 0o644); err != nil {
		t.Fatalf("write shapes.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(main), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	return dir
}

func runFernInterp(t *testing.T, file string) (int, string) {
	t.Helper()
	bin := buildLangBinForInterp(t)
	cmd := exec.Command(bin, "-interp", file)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	return cmd.ProcessState.ExitCode(), out.String() + errb.String()
}

func TestInterpCrossModuleTraitDirect(t *testing.T) {
	dir := writeTraitProject(t, `import "./shapes";
function main(): i32 {
    var sq: shapes.Square = shapes.Square { side: 5 };
    return sq.area();
}
`)
	code, output := runFernInterp(t, filepath.Join(dir, "main.fern"))
	if code != 25 {
		t.Errorf("exit = %d, want 25 (5*5)\n%s", code, output)
	}
}

func TestInterpCrossModuleTraitBoundedGeneric(t *testing.T) {
	dir := writeTraitProject(t, `import "./shapes";
function describe[T: shapes.Area](v: T): i32 { return v.area(); }
function main(): i32 {
    var sq: shapes.Square = shapes.Square { side: 4 };
    return describe(sq);
}
`)
	code, output := runFernInterp(t, filepath.Join(dir, "main.fern"))
	if code != 16 {
		t.Errorf("exit = %d, want 16 (4*4)\n%s", code, output)
	}
}

// The orphan rule is enforced across modules: an impl in a module that
// owns neither the trait nor the type is rejected, with the diagnostic
// reading the user's `mod.Name` spelling (not the internal mangling).
func TestInterpCrossModuleOrphanRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tr.fern"), []byte(`pub trait Show { function show(self: Self): i32; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ty.fern"), []byte(`pub struct Widget { id: i32 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.fern"), []byte(`import "./tr";
import "./ty";
impl tr.Show for ty.Widget { function show(self: Self): i32 { return self.id; } }
function main(): i32 { return 0; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, output := runFernInterp(t, filepath.Join(dir, "main.fern"))
	if code == 0 {
		t.Errorf("orphan impl should be rejected, got exit 0\n%s", output)
	}
	if !strings.Contains(output, "orphan impl") || !strings.Contains(output, "tr.Show") {
		t.Errorf("expected orphan diagnostic naming `tr.Show`, got:\n%s", output)
	}
}
