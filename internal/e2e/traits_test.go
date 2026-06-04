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

// @derive(Eq, Display, Ord) synthesises field-wise impls: the generated
// methods call the trait method on each field, so derivation composes
// (a nested struct field only needs to itself derive/impl the trait).
// See docs/TRAITS.md.
func TestInterpDeriveStructTraits(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";

@derive(cmp.Eq, cmp.Display)
struct Inner { n: i32 }

@derive(cmp.Eq, cmp.Display, cmp.Ord)
struct Point { x: i32, y: i32 }

@derive(cmp.Eq, cmp.Display)
struct Outer { a: Inner, tag: string }

function show[T: cmp.Display](v: T): string { return v.to_string(); }

function main(): i32 {
    var a: Point = Point { x: 3, y: 7 };
    var b: Point = Point { x: 3, y: 7 };
    var c: Point = Point { x: 3, y: 9 };
    print(show(a));
    if (a.eq(b)) { print("a-eq-b"); }
    if (!a.eq(c)) { print("a-neq-c"); }
    print(a.cmp(c).to_string());
    // Composition: Outer.eq recurses into Inner.eq.
    var p: Outer = Outer { a: Inner { n: 5 }, tag: "hi" };
    var q: Outer = Outer { a: Inner { n: 5 }, tag: "hi" };
    if (p.eq(q)) { print("outer-eq"); }
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
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"Point { x: 3, y: 7 }", "a-eq-b", "a-neq-c", "-1", "outer-eq", "Outer { a: Inner { n: 5 }, tag: hi }"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s\nstderr: %s", want, got, errb.String())
		}
	}
}

// @derive(Eq, Display) on an ENUM synthesises variant-wise match-based
// methods: Eq compares the same variant's payloads field-wise (any
// other variant is unequal); Display renders `Variant(payload, …)`.
// See docs/TRAITS.md (Phase 4, enums).
func TestInterpDeriveEnumTraits(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";

@derive(cmp.Eq, cmp.Display)
enum Shape {
    Circle(i32),
    Rect(i32, i32),
    Dot,
}

function main(): i32 {
    var a: Shape = Rect(3, 4);
    var b: Shape = Rect(3, 4);
    var c: Shape = Circle(5);
    print(a.to_string());
    print(c.to_string());
    print(Dot.to_string());
    if (a.eq(b)) { print("a-eq-b"); }
    if (!a.eq(c)) { print("a-neq-c"); }
    if (!c.eq(Dot)) { print("c-neq-dot"); }
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
	for _, want := range []string{"Rect(3, 4)", "Circle(5)", "Dot", "a-eq-b", "a-neq-c", "c-neq-dot"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s\nstderr: %s", want, got, errb.String())
		}
	}
}

// The collapsed generic array assertions (assert_eq_array / assert_at /
// assert_array_contains / assert_array_not_contains) work over any
// Eq+Display element type. See docs/TRAITS.md.
func TestInterpGenericArrayAsserts(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/test";
function main(): i32 {
    var r: test.TestRunner = test.test_new("arr");
    r = r.it("eq i32[]", test.assert_eq_array([1, 2, 3], [1, 2, 3]));
    r = r.it("eq string[]", test.assert_eq_array(["a", "b"], ["a", "b"]));
    r = r.it("at", test.assert_at([10, 20, 30], 1, 20));
    r = r.it("contains", test.assert_array_contains(["x", "y"], "y"));
    r = r.it("not_contains", test.assert_array_not_contains([1, 2], 9));
    return r.finish();
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
		t.Errorf("exit = %d, want 0 (all asserts pass)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if got := out.String(); !strings.Contains(got, "# pass 5") || strings.Contains(got, "not ok") {
		t.Errorf("expected 5 passing asserts; got:\n%s\nstderr: %s", got, errb.String())
	}
}

// @derive(Ord) on an enum: a variant declared earlier sorts before a
// later one; within a variant, payloads compare lexicographically. Also
// exercises `impl Ord for string` (via a string payload). See
// docs/TRAITS.md.
func TestInterpDeriveEnumOrd(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";
@derive(cmp.Ord)
enum Level { Low(i32), Mid(string), High }
function sign(n: i32): string { if (n < 0) { return "lt"; } if (n > 0) { return "gt"; } return "eq"; }
function main(): i32 {
    print(sign(Low(1).cmp(Low(2))));    // lt
    print(sign(Low(9).cmp(Mid("a"))));  // lt  (Low variant before Mid)
    print(sign(High.cmp(Low(0))));      // gt  (High variant after Low)
    print(sign(Mid("b").cmp(Mid("b")))); // eq
    print(sign(Mid("apple").cmp(Mid("banana")))); // lt (string lex)
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
	if got := out.String(); got != "lt\nlt\ngt\neq\nlt\n" {
		t.Errorf("enum Ord output = %q, want lt/lt/gt/eq/lt", got)
	}
}

// Opaque types: a `pub opaque struct` exports its name + methods but
// keeps fields private outside the declaring module — field reads and
// struct-literal construction from another module are rejected; a
// factory function + methods work. See docs/TRAITS.md.
func TestOpaqueTypeEncapsulation(t *testing.T) {
	dir := t.TempDir()
	lib := `pub opaque struct Email { addr: string }
pub function make(a: string): Email { return Email { addr: a }; }
pub function (e: Email) domain(): string { return e.addr; }
`
	if err := os.WriteFile(filepath.Join(dir, "email.fern"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Legal: construct via factory, call method.
	okMain := write("ok_main.fern", `import "./email";
function main(): i32 { var e: email.Email = email.make("a@b.com"); print(e.domain()); return 0; }
`)
	if code, out := runFernInterp(t, okMain); code != 0 || !strings.Contains(out, "a@b.com") {
		t.Errorf("legal opaque use failed: exit=%d out=%q", code, out)
	}
	// Illegal: field read from another module.
	badField := write("bad_field.fern", `import "./email";
function main(): i32 { var e: email.Email = email.make("x"); print(e.addr); return 0; }
`)
	if code, out := runFernInterp(t, badField); code == 0 || !strings.Contains(out, "opaque type email.Email") {
		t.Errorf("field read of opaque type should be rejected: exit=%d out=%q", code, out)
	}
	// Illegal: construction from another module.
	badCtor := write("bad_ctor.fern", `import "./email";
function main(): i32 { var e: email.Email = email.Email { addr: "x" }; return 0; }
`)
	if code, out := runFernInterp(t, badCtor); code == 0 || !strings.Contains(out, "construct opaque type") {
		t.Errorf("construction of opaque type should be rejected: exit=%d out=%q", code, out)
	}
}
