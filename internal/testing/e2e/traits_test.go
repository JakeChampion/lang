package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
    let p: Point = Point { x: 3, y: 7 };
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
    let d: Dog = Dog { age: 2 };
    let n: i32 = 5;
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
    let p: Point = Point { x: 9, y: 2 };
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
    let sq: shapes.Square = shapes.Square { side: 5 };
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
    let sq: shapes.Square = shapes.Square { side: 4 };
    return describe(sq);
}
`)
	code, output := runFernInterp(t, filepath.Join(dir, "main.fern"))
	if code != 16 {
		t.Errorf("exit = %d, want 16 (4*4)\n%s", code, output)
	}
}

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
    let a: Point = Point { x: 3, y: 7 };
    let b: Point = Point { x: 3, y: 7 };
    let c: Point = Point { x: 3, y: 9 };
    print(show(a));
    if (a.eq(b)) { print("a-eq-b"); }
    if (!a.eq(c)) { print("a-neq-c"); }
    print(a.cmp(c).to_string());
    // Composition: Outer.eq recurses into Inner.eq.
    let p: Outer = Outer { a: Inner { n: 5 }, tag: "hi" };
    let q: Outer = Outer { a: Inner { n: 5 }, tag: "hi" };
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
    let a: Shape = Rect(3, 4);
    let b: Shape = Rect(3, 4);
    let c: Shape = Circle(5);
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

func TestInterpGenericArrayAsserts(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "std/test";
function main(): i32 {
    let r: test.TestRunner = test.test_new("arr");
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

func TestInterpGenericMapAsserts(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(`import "core/map";
import "std/test";
function main(): i32 {
    let a: Map[i32, i32] = map_new(4);
    a = a.insert(1, 10); a = a.insert(2, 20);
    let s: Map[string, string] = map_new(4);
    s = s.insert("k", "v");
    let r: test.TestRunner = test.test_new("map");
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
	okMain := write("ok_main.fern", `import "./email";
function main(): i32 { let e: email.Email = email.make("a@b.com"); print(e.domain()); return 0; }
`)
	if code, out := runFernInterp(t, okMain); code != 0 || !strings.Contains(out, "a@b.com") {
		t.Errorf("legal opaque use failed: exit=%d out=%q", code, out)
	}
	badField := write("bad_field.fern", `import "./email";
function main(): i32 { let e: email.Email = email.make("x"); print(e.addr); return 0; }
`)
	if code, out := runFernInterp(t, badField); code == 0 || !strings.Contains(out, "opaque type email.Email") {
		t.Errorf("field read of opaque type should be rejected: exit=%d out=%q", code, out)
	}
	badCtor := write("bad_ctor.fern", `import "./email";
function main(): i32 { let e: email.Email = email.Email { addr: "x" }; return 0; }
`)
	if code, out := runFernInterp(t, badCtor); code == 0 || !strings.Contains(out, "construct opaque type") {
		t.Errorf("construction of opaque type should be rejected: exit=%d out=%q", code, out)
	}
	patterns := []struct{ name, body string }{
		{"bad_match.fern", `import "./email";
function main(): i32 {
    let e: email.Email = email.make("x");
    match (e) { email.Email { addr } => { print(addr); return 0; } }
}
`},
		{"bad_iflet.fern", `import "./email";
function main(): i32 {
    let e: email.Email = email.make("x");
    if let email.Email { addr } = e { print(addr); }
    return 0;
}
`},
		{"bad_match_expr.fern", `import "./email";
function main(): i32 {
    let e: email.Email = email.make("x");
    let s: string = match (e) { email.Email { addr } => addr };
    print(s);
    return 0;
}
`},
	}
	for _, p := range patterns {
		src := write(p.name, p.body)
		if code, out := runFernInterp(t, src); code == 0 || !strings.Contains(out, "opaque type email.Email") {
			t.Errorf("%s: destructuring an opaque type should be rejected: exit=%d out=%q", p.name, code, out)
		}
	}
	okRest := write("ok_rest.fern", `import "./email";
function main(): i32 {
    let e: email.Email = email.make("x");
    match (e) { email.Email { .. } => { print("ok"); return 0; } }
}
`)
	if code, out := runFernInterp(t, okRest); code != 0 || !strings.Contains(out, "ok") {
		t.Errorf("`S { .. }` binds nothing and must stay legal: exit=%d out=%q", code, out)
	}
	okInside := write("ok_inside.fern", `pub opaque struct Email { addr: string }
pub function make(a: string): Email { return Email { addr: a }; }
function main(): i32 {
    let e: Email = make("a@b.com");
    match (e) { Email { addr } => { print(addr); return 0; } }
}
`)
	if code, out := runFernInterp(t, okInside); code != 0 || !strings.Contains(out, "a@b.com") {
		t.Errorf("the defining module must still destructure its own opaque type: exit=%d out=%q", code, out)
	}
}

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
    let shapes: dyn Shape[] = [Circle { r: 2 }, Rect { w: 3, h: 4 }, Circle { r: 1 }];
    let total: i32 = 0;
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
	gen := exec.Command(bin, "-target", "arm64-linux", "-o", filepath.Join(dir, "out"), src)
	var gerr bytes.Buffer
	gen.Stderr = &gerr
	if err := gen.Run(); err != nil {
		t.Errorf("compiling dyn Trait to arm64 should now succeed, but it failed: %v\nstderr: %s", err, gerr.String())
	}
	if strings.Contains(gerr.String(), "dyn Trait is not yet supported on compiled backends") {
		t.Errorf("arm64 should no longer reject dyn; got the stale unsupported-feature diagnostic: %s", gerr.String())
	}
}

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
    let one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    let items: dyn Show + Weigh[] = [Apple { g: 120 }, Brick { kg: 2 }];
    let total: i32 = 0;
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
	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
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
    let c: Option[Circle] = s as? Circle;
    return match (c) {
        Some(x) => "circle r=" + x.r.to_string(),
        None => "other",
    };
}
function main(): i32 {
    let shapes: dyn Shape[] = [Circle { r: 5 }, Rect { w: 2, h: 3 }, Circle { r: 9 }];
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

	for _, target := range []string{"arm64-linux", "x86-64-linux", "wasm32-wasi"} {
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

func TestInterpDynTraitQualifiedTraitName(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `import "std/i32";
import "std/string";
import "core/cmp";
function render(args: dyn cmp.Display[]): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < args.len()) {
        out = out + args[i].to_string();
        if (i + 1 < args.len()) { out = out + ", "; }
        i = i + 1;
    }
    return out;
}
function main(): i32 {
    let xs: dyn cmp.Display[] = [42, "hi", true];
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
    let a = Box { v: 42 };
    let s = Box { v: "hi" };
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
    let b = Box { v: NoDisp {} };
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
    let t = Leaf(42);
    print(t.to_string());
    print(Pair(1, 2).to_string());
    if (t.eq(Leaf(42))) { print("leaf-eq"); }
    if (!t.eq(Pair(1, 2))) { print("t-neq"); }
    print(sign(Leaf(1).cmp(Pair(0, 0)))); // lt (Leaf variant before Pair)

    let p = Twin { a: 7, b: "x" };
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

func TestArm64ParametricImplAndDerive(t *testing.T) {
	src := `import "core/cmp";

struct Box[T] { v: T }
impl[T: cmp.Display] cmp.Display for Box[T] {
    function to_string(self: Self): string { return "Box(" + self.v.to_string() + ")"; }
}

@derive(cmp.Display, cmp.Eq)
enum Opt[T] { Has(T), Nil }

function main(): i32 {
    let a = Box { v: 5 };
    let s = Box { v: "hi" };
    print(a.to_string());
    print(s.to_string());
    print(Has(9).to_string());
    let n: Opt[i32] = Nil;
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
    let d: Dog = Dog { age: 3 };
    let c: Cat = Cat { age: 5 };
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
	if strings.Count(got, "hello, rex") != 2 {
		t.Errorf("expected inherited default reached twice (direct + via bound), got: %q", got)
	}
}

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
    let d: Dog = Dog { age: 3 };
    let c: Cat = Cat { age: 5 };
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
    let p: P = P { x: 3 };
    let q: P = P { x: 5 };
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

func TestWASMTraitSupertrait(t *testing.T) {
	got := runWasmCapturingStdout(t, traitSupertraitSrc)
	for _, want := range []string{"lt", "gt", "eq"} {
		if !strings.Contains(got, want) {
			t.Errorf("wasm output missing %q; got:\n%s", want, got)
		}
	}
}

const traitSupertraitDirectSrc = `import "std/i32";

trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }

struct P { x: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }

function main(): i32 {
    let p: P = P { x: 3 };
    let q: P = P { x: 5 };
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

const valueSpelledLikeTypeParamSrc = `import "core/cmp";
function same[T: cmp.Eq](T: T, u: T): i32 { if (T.eq(u)) { return 1; } return 0; }
function main(): i32 {
    print("same=" + (same(3, 3) + same("a", "b") * 10).to_string());
    return 0;
}
`

func TestInterpValueSpelledLikeTypeParam(t *testing.T) {
	src := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(src, []byte(valueSpelledLikeTypeParamSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	code, out := runFernInterp(t, src)
	if code != 0 || strings.TrimSpace(out) != "same=1" {
		t.Fatalf("exit = %d, output %q; want 0 and \"same=1\"", code, out)
	}
}

func TestArm64ValueSpelledLikeTypeParam(t *testing.T) {
	out, code := compileAndRunArm64(t, valueSpelledLikeTypeParamSrc)
	if code != 0 || strings.TrimSpace(out) != "same=1" {
		t.Fatalf("exit = %d, output %q; want 0 and \"same=1\"", code, out)
	}
}

func TestWASMValueSpelledLikeTypeParam(t *testing.T) {
	if got := strings.TrimSpace(runWasmCapturingStdout(t, valueSpelledLikeTypeParamSrc)); got != "same=1" {
		t.Fatalf("output %q; want \"same=1\"", got)
	}
}
