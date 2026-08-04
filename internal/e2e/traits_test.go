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
    r = r.it("eq i32[]", () => test.assert_eq_array([1, 2, 3], [1, 2, 3]));
    r = r.it("eq string[]", () => test.assert_eq_array(["a", "b"], ["a", "b"]));
    r = r.it("at", () => test.assert_at([10, 20, 30], 1, 20));
    r = r.it("contains", () => test.assert_array_contains(["x", "y"], "y"));
    r = r.it("not_contains", () => test.assert_array_not_contains([1, 2], 9));
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

// The collapsed generic `Map[K, V]` assertions (assert_map_len /
// assert_map_has / assert_map_lacks / assert_eq_map) work over any
// K/V whose types implement Eq + Display. Exercises both an i32-keyed
// and a string-keyed map from the SAME generic helpers — the multi-
// parameter monomorphiser clones one concrete helper per K/V pair. See
// docs/TRAITS.md §7a.
func TestInterpGenericMapAsserts(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/map";
import "std/test";
function main(): i32 {
    var a: Map[i32, i32] = map_new(4);
    a = a.insert(1, 10); a = a.insert(2, 20);
    var s: Map[string, string] = map_new(4);
    s = s.insert("k", "v");
    var r: test.TestRunner = test.test_new("map");
    r = r.it("len i32",       () => test.assert_map_len(a, 2));
    r = r.it("has i32",       () => test.assert_map_has(a, 1, 10));
    r = r.it("lacks i32",     () => test.assert_map_lacks(a, 9));
    r = r.it("len string",    () => test.assert_map_len(s, 1));
    r = r.it("has string",    () => test.assert_map_has(s, "k", "v"));
    r = r.it("eq i32",        () => test.assert_eq_map(a, a));
    r = r.it("eq string",     () => test.assert_eq_map(s, s));
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
	if got := out.String(); !strings.Contains(got, "# pass 7") || strings.Contains(got, "not ok") {
		t.Errorf("expected 7 passing map asserts; got:\n%s\nstderr: %s", got, errb.String())
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

// `dyn Trait` (runtime trait objects): a heterogeneous `dyn Shape[]`
// holding two different concrete types dispatches each element's method
// by runtime type on the interpreter. Compiled backends reject `dyn`
// with a clean unsupported-feature message (interpreter-only in slice
// 1). See docs/DYN-TRAITS.md.
func TestInterpDynTraitDispatch(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
    function name(self: Self): string;
}
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r * 3; }
    function name(self: Self): string { return "circle"; }
}
impl Shape for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
    function name(self: Self): string { return "rect"; }
}
function describe(s: dyn Shape): string {
    return s.name() + "=" + s.area().to_string();
}
function main(): i32 {
    var shapes: dyn Shape[] = [Circle { r: 2 }, Rect { w: 3, h: 4 }, Circle { r: 1 }];
    var total: i32 = 0;
    for s in shapes {
        print(describe(s));
        total = total + s.area();
    }
    print("total=" + total.to_string());
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Interpreter: heterogeneous dynamic dispatch works.
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	for _, want := range []string{"circle=12", "rect=12", "circle=3", "total=27"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("interp output missing %q; got:\n%s", want, out.String())
		}
	}
	// All three compiled backends now LOWER `dyn` via the boxed
	// one-word representation (wasm: inline two-word, slice 2b; x86-64:
	// slice 2c; arm64: slice 2d — docs/DYN-TRAITS.md §4.2/§7). No
	// compiled backend rejects `dyn` anymore. Assert arm64 compiles
	// CLEANLY (no unsupported-feature diagnostic, no crash); the
	// differential run-vs-interp coverage lives in TestArm64DynTrait* /
	// TestX86_64DynTrait* / TestWASMDynTrait* in dyn_trait_compiled_test.go.
	gen := exec.Command(bin, "-target", "arm64", "-o", filepath.Join(dir, "out"), src)
	var gerr bytes.Buffer
	gen.Stderr = &gerr
	if err := gen.Run(); err != nil {
		t.Errorf("compiling dyn Trait to arm64 should now succeed, but it failed: %v\nstderr: %s", err, gerr.String())
	}
	if strings.Contains(gerr.String(), "dyn Trait is not yet supported on compiled backends") {
		t.Errorf("arm64 should no longer reject dyn; got the stale unsupported-feature diagnostic: %s", gerr.String())
	}
}

// TestInterpDynMultiTrait exercises a MULTI-trait trait object
// (`dyn Show + Eq`, slice 1 of multi-trait `dyn` — docs/DYN-TRAITS.md):
// a struct implementing BOTH traits coerces to `dyn Show + Eq` and can
// call a method from EACH trait, dispatched by the value's runtime
// concrete type. A heterogeneous `dyn Show + Eq[]` iterates + dispatches.
// The compiled backends REJECT multi-trait `dyn` cleanly (merged-vtable
// codegen is a later slice) — assert each target rejects with the clean
// message, no panic.
func TestInterpDynMultiTrait(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/i32";
trait Show { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
struct Brick { kg: i32 }
impl Show for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }
impl Show for Brick { function show(self: Self): string { return "brick"; } }
impl Weigh for Brick { function weight(self: Self): i32 { return self.kg * 1000; } }
function describe(d: dyn Show + Weigh): string {
    // a method from EACH trait, on the same multi-trait object
    return d.show() + "=" + d.weight().to_string();
}
function main(): i32 {
    // order-insensitive: dyn Weigh + Show is the same type
    var one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    var items: dyn Show + Weigh[] = [Apple { g: 120 }, Brick { kg: 2 }];
    var total: i32 = 0;
    for it in items {
        print(describe(it));
        total = total + it.weight();
    }
    print("total=" + total.to_string());
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Interpreter: multi-trait dispatch from both traits works.
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	for _, want := range []string{"apple=150", "apple=120", "brick=2000", "total=2120"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("interp output missing %q; got:\n%s", want, out.String())
		}
	}
	// Compiled backends now LOWER multi-trait `dyn A + B` dispatch through
	// the merged vtable (docs/DYN-TRAITS.md §10) — codegen must succeed (no
	// reject, no panic). The differential correctness tests against the
	// interpreter live in dyn_trait_compiled_test.go (dynAllBackends).
	for _, target := range []string{"arm64", "x86-64", "wasm"} {
		gen := exec.Command(bin, "-target", target, "-o", filepath.Join(dir, "out_"+target), src)
		var gerr bytes.Buffer
		gen.Stderr = &gerr
		err := gen.Run()
		if err != nil {
			t.Errorf("compiling multi-trait `dyn` dispatch to %s should now succeed, got error:\n%s", target, gerr.String())
		}
		if strings.Contains(gerr.String(), "panic") {
			t.Errorf("%s multi-trait codegen panicked: %s", target, gerr.String())
		}
	}
}

// TestInterpDowncast exercises the `e as? T` fallible downcast
// (docs/DYN-TRAITS.md §9) on the interpreter: a `dyn Shape` holding a
// Circle downcasts to Some(circle) (and the bound value is usable as the
// concrete Circle), and to None for Rect. A heterogeneous `dyn Shape[]`
// downcasts each element. The compiled backends now LOWER the downcast
// (vtable-pointer compare); this test asserts each target compiles
// without error — the per-backend differential-vs-interp behaviour lives
// in TestDowncast* (dyn_trait_compiled_test.go).
func TestInterpDowncast(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
function describe(s: dyn Shape): string {
    // hit binds the concrete value, usable as a Circle (reads .r)
    var c: Option[Circle] = s as? Circle;
    return match (c) {
        Some(x) => "circle r=" + x.r.to_string(),
        None => "other",
    };
}
function main(): i32 {
    var shapes: dyn Shape[] = [Circle { r: 5 }, Rect { w: 2, h: 3 }, Circle { r: 9 }];
    for s in shapes {
        print(describe(s));
    }
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("interp exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	for _, want := range []string{"circle r=5", "other", "circle r=9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("interp output missing %q; got:\n%s", want, out.String())
		}
	}

	// Compiled backends now LOWER the downcast (vtable-pointer compare) —
	// compiling to each target must succeed (no error, no panic). The
	// runtime-behaviour differential vs the interpreter is in TestDowncast*
	// (dyn_trait_compiled_test.go).
	for _, target := range []string{"arm64", "x86-64", "wasm"} {
		gen := exec.Command(bin, "-target", target, "-o", filepath.Join(dir, "out_"+target), src)
		var gerr bytes.Buffer
		gen.Stderr = &gerr
		err := gen.Run()
		if err != nil {
			t.Errorf("compiling `as?` downcast to %s should now succeed, but failed: %v\nstderr: %s", target, err, gerr.String())
		}
		if strings.Contains(gerr.String(), "panic") {
			t.Errorf("%s downcast codegen panicked: %s", target, gerr.String())
		}
	}
}

// A `dyn` type may name a *module-qualified* imported trait
// (`dyn cmp.Display`), not just a locally-declared one. Before the
// modload fix, the qualified trait name in the `dyn` type kept its
// dotted form and never matched the mangled `cmp__Display` key in
// Info.Traits, so it failed with "unknown trait". A heterogeneous
// `dyn cmp.Display[]` of scalars dispatches `to_string()` by runtime
// type on the interpreter.
func TestInterpDynTraitQualifiedTraitName(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/i32";
import "std/string";
import "core/cmp";
function render(args: dyn cmp.Display[]): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < args.len()) {
        out = out + args[i].to_string();
        if (i + 1 < args.len()) { out = out + ", "; }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    var xs: dyn cmp.Display[] = [42, "hi", true];
    print(render(xs));
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "42, hi, true" {
		t.Errorf("dyn cmp.Display dispatch = %q, want %q", got, "42, hi, true")
	}
}

// Parametric impl: `impl[T: Bound] Trait for Box[T]` makes a single
// blanket impl cover every instantiation. The method bodies dispatch
// on the bound (`self.v.to_string()` where `self.v: T`), and the
// generic methods monomorphise per instantiation. The same `Box`
// implements Display for i32 and string payloads from one impl block.
// See docs/TRAITS.md.
func TestInterpParametricImpl(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";

struct Box[T] { v: T }

impl[T: cmp.Display] cmp.Display for Box[T] {
    function to_string(self: Self): string {
        return "Box(" + self.v.to_string() + ")";
    }
}

impl[T: cmp.Eq] cmp.Eq for Box[T] {
    function eq(self: Self, other: Self): boolean {
        return self.v.eq(other.v);
    }
}

function main(): i32 {
    var a = Box { v: 42 };
    var s = Box { v: "hi" };
    print(a.to_string());
    print(s.to_string());
    if (a.eq(Box { v: 42 })) { print("a-eq"); }
    if (!a.eq(Box { v: 7 })) { print("a-neq"); }
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
	for _, want := range []string{"Box(42)", "Box(hi)", "a-eq", "a-neq"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s\nstderr: %s", want, got, errb.String())
		}
	}
}

// A parametric impl that violates the bound is rejected: `Box[NoDisp]`
// where `NoDisp` has no Display impl can't satisfy `[T: Display]`. The
// diagnostic names the offending method as `Box.to_string`.
func TestInterpParametricImplBoundRejected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";
struct NoDisp {}
struct Box[T] { v: T }
impl[T: cmp.Display] cmp.Display for Box[T] {
    function to_string(self: Self): string { return self.v.to_string(); }
}
function main(): i32 {
    var b = Box { v: NoDisp {} };
    print(b.to_string());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	code, out := runFernInterp(t, src)
	if code == 0 || !strings.Contains(out, "does not implement trait") || !strings.Contains(out, "Box.to_string") {
		t.Errorf("bound violation should be rejected with Box.to_string: exit=%d out=%q", code, out)
	}
}

// @derive on a GENERIC struct / enum synthesises a parametric impl
// (`impl[T: Trait] Trait for Box[T]`): the field/variant-wise body
// dispatches through the per-param bound and monomorphises per
// instantiation. Covers Eq, Display, Ord on a generic enum plus a
// two-parameter generic struct. See docs/TRAITS.md.
func TestInterpDeriveGenericTypes(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/cmp";

@derive(cmp.Display, cmp.Eq, cmp.Ord)
enum Tree[T] { Leaf(T), Pair(T, T), Empty }

@derive(cmp.Display, cmp.Eq)
struct Twin[A, B] { a: A, b: B }

function sign(n: i32): string { if (n < 0) { return "lt"; } if (n > 0) { return "gt"; } return "eq"; }

function main(): i32 {
    var t = Leaf(42);
    print(t.to_string());
    print(Pair(1, 2).to_string());
    if (t.eq(Leaf(42))) { print("leaf-eq"); }
    if (!t.eq(Pair(1, 2))) { print("t-neq"); }
    print(sign(Leaf(1).cmp(Pair(0, 0)))); // lt (Leaf variant before Pair)

    var p = Twin { a: 7, b: "x" };
    print(p.to_string());
    if (p.eq(Twin { a: 7, b: "x" })) { print("twin-eq"); }
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
	for _, want := range []string{"Leaf(42)", "Pair(1, 2)", "leaf-eq", "t-neq", "lt", "Twin { a: 7, b: x }", "twin-eq"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s\nstderr: %s", want, got, errb.String())
		}
	}
}

// Parametric impls + generic derive must hold on a compiled backend,
// not just the interpreter — the generic methods monomorphise and the
// native codegen handles the per-instantiation clones. Exercises arm64
// (the default target) end-to-end.
func TestArm64ParametricImplAndDerive(t *testing.T) {
	src := `import "core/cmp";

struct Box[T] { v: T }
impl[T: cmp.Display] cmp.Display for Box[T] {
    function to_string(self: Self): string { return "Box(" + self.v.to_string() + ")"; }
}

@derive(cmp.Display, cmp.Eq)
enum Opt[T] { Has(T), Nil }

function main(): i32 {
    var a = Box { v: 5 };
    var s = Box { v: "hi" };
    print(a.to_string());
    print(s.to_string());
    print(Has(9).to_string());
    var n: Opt[i32] = Nil;
    print(n.to_string());
    if (Has(9).eq(Has(9))) { print("has-eq"); }
    return 0;
}
`
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"Box(5)", "Box(hi)", "Has(9)", "Nil", "has-eq"} {
		if !strings.Contains(out, want) {
			t.Errorf("arm64 output missing %q; got:\n%s", want, out)
		}
	}
}

// A trait method with a default body is inherited by an impl that omits
// it and may be overridden by one that provides its own. Both forms
// dispatch through the interpreter, including when reached via a
// trait-bounded generic. See docs/TRAITS.md.
const traitDefaultMethodSrc = `import "std/i32";

trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hello, " + self.name(); }
}

struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }

struct Cat { age: i32 }
impl Greet for Cat {
    function name(self: Self): string { return "felix"; }
    function greeting(self: Self): string { return "meow from " + self.name(); }
}

function announce[T: Greet](x: T): string { return x.greeting(); }

function main(): i32 {
    var d: Dog = Dog { age: 3 };
    var c: Cat = Cat { age: 5 };
    print(d.greeting());       // inherited default
    print(c.greeting());       // overridden
    print(announce(d));        // default via bound
    return 0;
}
`

func TestInterpTraitDefaultMethod(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(traitDefaultMethodSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"hello, rex", "meow from felix"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q; got: %q (stderr: %s)", want, got, errb.String())
		}
	}
	// `announce(d)` reuses the inherited default → "hello, rex" appears twice.
	if strings.Count(got, "hello, rex") != 2 {
		t.Errorf("expected inherited default reached twice (direct + via bound), got: %q", got)
	}
}

// Default methods compile + run natively: they desugar to ordinary
// receiver methods, so codegen needs no trait awareness. The x86-64 e2e
// helper doesn't run the monomorph pass, so this variant uses direct
// dispatch (inherited default + override); the bounded-generic path is
// covered natively by the arm64 test below and on the interpreter.
const traitDefaultMethodDirectSrc = `import "std/i32";

trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hello, " + self.name(); }
}

struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }

struct Cat { age: i32 }
impl Greet for Cat {
    function name(self: Self): string { return "felix"; }
    function greeting(self: Self): string { return "meow from " + self.name(); }
}

function main(): i32 {
    var d: Dog = Dog { age: 3 };
    var c: Cat = Cat { age: 5 };
    print(d.greeting());       // inherited default
    print(c.greeting());       // overridden
    return 0;
}
`

func TestX86_64TraitDefaultMethod(t *testing.T) {
	out, code := compileAndRunX86_64(t, traitDefaultMethodDirectSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"hello, rex", "meow from felix"} {
		if !strings.Contains(out, want) {
			t.Errorf("x86-64 output missing %q; got:\n%s", want, out)
		}
	}
}

// arm64 native default-method coverage, including the bounded-generic
// path (`announce[T: Greet]` → inherited default) which the arm64 helper
// monomorphises like the production driver.
func TestArm64TraitDefaultMethod(t *testing.T) {
	out, code := compileAndRunArm64(t, traitDefaultMethodSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"hello, rex", "meow from felix"} {
		if !strings.Contains(out, want) {
			t.Errorf("arm64 output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Count(out, "hello, rex") != 2 {
		t.Errorf("expected inherited default reached twice (direct + via bound), got:\n%s", out)
	}
}

// wasm default-method coverage, including the bounded-generic path — the
// wasm component builder monomorphises like the production driver.
func TestWASMTraitDefaultMethod(t *testing.T) {
	got := runWasmCapturingStdout(t, traitDefaultMethodSrc)
	for _, want := range []string{"hello, rex", "meow from felix"} {
		if !strings.Contains(got, want) {
			t.Errorf("wasm output missing %q; got:\n%s", want, got)
		}
	}
	if strings.Count(got, "hello, rex") != 2 {
		t.Errorf("expected inherited default reached twice (direct + via bound), got:\n%s", got)
	}
}

// Supertraits (`trait Ord: Eq`): a `T: Ord` bound can call the
// supertrait Eq's method on T, and the impl-time check requires every
// `impl Ord for P` to also have `impl Eq for P`. The generic `rank`
// exercises supertrait dispatch through monomorphisation. See
// docs/TRAITS.md.
const traitSupertraitSrc = `import "std/i32";

trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }

struct P { x: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }

function rank[T: Ord](a: T, b: T): string {
    if (a.eq(b)) { return "eq"; }   // Eq method via the Ord supertrait bound
    if (a.lt(b)) { return "lt"; }
    return "gt";
}

function main(): i32 {
    var p: P = P { x: 3 };
    var q: P = P { x: 5 };
    print(rank(p, q));   // lt
    print(rank(q, p));   // gt
    print(rank(p, p));   // eq
    return 0;
}
`

func TestInterpTraitSupertrait(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(traitSupertraitSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	got := out.String()
	for _, want := range []string{"lt", "gt", "eq"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q; got: %q (stderr: %s)", want, got, errb.String())
		}
	}
}

// arm64 native supertrait coverage (the arm64 helper monomorphises the
// `rank[T: Ord]` generic like the production driver).
func TestArm64TraitSupertrait(t *testing.T) {
	out, code := compileAndRunArm64(t, traitSupertraitSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"lt", "gt", "eq"} {
		if !strings.Contains(out, want) {
			t.Errorf("arm64 output missing %q; got:\n%s", want, out)
		}
	}
}

// wasm supertrait coverage (the wasm component builder monomorphises).
func TestWASMTraitSupertrait(t *testing.T) {
	got := runWasmCapturingStdout(t, traitSupertraitSrc)
	for _, want := range []string{"lt", "gt", "eq"} {
		if !strings.Contains(got, want) {
			t.Errorf("wasm output missing %q; got:\n%s", want, got)
		}
	}
}

// Direct (non-generic) supertrait dispatch, for the x86-64 e2e helper
// (which doesn't run the monomorph pass). Still exercises supertrait
// conformance (`impl Ord for P` requires `impl Eq for P`) and dispatch of
// both the trait's own and the supertrait's methods on a concrete value.
const traitSupertraitDirectSrc = `import "std/i32";

trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }

struct P { x: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }

function main(): i32 {
    var p: P = P { x: 3 };
    var q: P = P { x: 5 };
    if (p.eq(q)) { print("eq"); } else { print("neq"); }   // neq
    if (p.lt(q)) { print("lt"); }                          // lt
    return 0;
}
`

func TestX86_64TraitSupertrait(t *testing.T) {
	out, code := compileAndRunX86_64(t, traitSupertraitDirectSrc)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"neq", "lt"} {
		if !strings.Contains(out, want) {
			t.Errorf("x86-64 output missing %q; got:\n%s", want, out)
		}
	}
}
