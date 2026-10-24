package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func dynInterpStdout(t *testing.T, src string) string {
	t.Helper()
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(bin, "-interp", p)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("interp exit = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	return strings.TrimSpace(out.String())
}

func TestWASMDynTraitStructDispatch(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
function main(): i32 {
    let d: dyn Shape = Circle { r: 5 };
    print("area=" + d.area().to_string());
    return 0;
}
`
	want := dynInterpStdout(t, src)
	got := runWasmCapturingStdout(t, src)
	if got != want {
		t.Errorf("wasm dyn dispatch = %q, want %q (interp)", got, want)
	}
	if want != "area=25" {
		t.Errorf("interp baseline = %q, want \"area=25\"", want)
	}
}

func TestWASMDynTraitHeterogeneousArray(t *testing.T) {
	src := `import "std/i32";
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
	want := dynInterpStdout(t, src)
	got := runWasmCapturingStdout(t, src)
	if got != want {
		t.Errorf("wasm dyn array dispatch =\n%q\nwant (interp):\n%q", got, want)
	}
	for _, line := range []string{"circle=12", "rect=12", "circle=3", "total=27"} {
		if !strings.Contains(got, line) {
			t.Errorf("wasm output missing %q; got:\n%s", line, got)
		}
	}
}

func TestX86_64DynTraitStructDispatch(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
function main(): i32 {
    let d: dyn Shape = Circle { r: 5 };
    print("area=" + d.area().to_string());
    return 0;
}
`
	want := dynInterpStdout(t, src)
	got, code := compileAndRunX86_64(t, src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("x86-64 dyn dispatch = %q, want %q (interp)", got, want)
	}
	if want != "area=25" {
		t.Errorf("interp baseline = %q, want \"area=25\"", want)
	}
}

func TestX86_64DynTraitHeterogeneousArray(t *testing.T) {
	src := `import "std/i32";
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
	want := dynInterpStdout(t, src)
	got, code := compileAndRunX86_64(t, src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("x86-64 dyn array dispatch =\n%q\nwant (interp):\n%q", got, want)
	}
	for _, line := range []string{"circle=12", "rect=12", "circle=3", "total=27"} {
		if !strings.Contains(got, line) {
			t.Errorf("x86-64 output missing %q; got:\n%s", line, got)
		}
	}
}

func TestArm64DynTraitStructDispatch(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
function main(): i32 {
    let d: dyn Shape = Circle { r: 5 };
    print("area=" + d.area().to_string());
    return 0;
}
`
	want := dynInterpStdout(t, src)
	got, code := compileAndRunArm64(t, src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("arm64 dyn dispatch = %q, want %q (interp)", got, want)
	}
	if want != "area=25" {
		t.Errorf("interp baseline = %q, want \"area=25\"", want)
	}
}

func TestArm64DynTraitHeterogeneousArray(t *testing.T) {
	src := `import "std/i32";
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
	want := dynInterpStdout(t, src)
	got, code := compileAndRunArm64(t, src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("arm64 dyn array dispatch =\n%q\nwant (interp):\n%q", got, want)
	}
	for _, line := range []string{"circle=12", "rect=12", "circle=3", "total=27"} {
		if !strings.Contains(got, line) {
			t.Errorf("arm64 output missing %q; got:\n%s", line, got)
		}
	}
}

const dynPrimI32Src = `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for i32 {
    function show(self: Self): i32 { return self + 100; }
}
function run(s: dyn Show): i32 { return s.show(); }
function main(): i32 {
    let x: i32 = 5;
    print("v=" + run(x).to_string());
    return 0;
}
`

func TestX86_64DynTraitPrimitiveReceiver(t *testing.T) {
	want := dynInterpStdout(t, dynPrimI32Src)
	got, code := compileAndRunX86_64(t, dynPrimI32Src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("x86-64 dyn-over-i32 = %q, want %q (interp)", got, want)
	}
	if want != "v=105" {
		t.Errorf("interp baseline = %q, want \"v=105\"", want)
	}
}

func TestArm64DynTraitPrimitiveReceiver(t *testing.T) {
	want := dynInterpStdout(t, dynPrimI32Src)
	got, code := compileAndRunArm64(t, dynPrimI32Src)
	got = strings.TrimSpace(got)
	if code != 0 {
		t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
	}
	if got != want {
		t.Errorf("arm64 dyn-over-i32 = %q, want %q (interp)", got, want)
	}
	if want != "v=105" {
		t.Errorf("interp baseline = %q, want \"v=105\"", want)
	}
}

func TestWASMDynTraitPrimitiveReceiver(t *testing.T) {
	want := dynInterpStdout(t, dynPrimI32Src)
	got := runWasmCapturingStdout(t, dynPrimI32Src)
	if got != want {
		t.Errorf("wasm dyn-over-i32 = %q, want %q (interp)", got, want)
	}
	if want != "v=105" {
		t.Errorf("interp baseline = %q, want \"v=105\"", want)
	}
}

func TestDynTraitPrimitiveI64(t *testing.T) {
	src := `import "std/i64";
trait Show {
    function show(self: Self): i64;
}
impl Show for i64 {
    function show(self: Self): i64 { return self + (1000 as i64); }
}
function run(s: dyn Show): i64 { return s.show(); }
function main(): i32 {
    let x: i64 = 9000000000 as i64;
    print("v=" + run(x).to_string());
    return 0;
}
`
	backendsAgree(t, src, "v=9000001000")
}

func TestDynTraitPrimitiveF64(t *testing.T) {
	src := `import "std/f64";
trait Show {
    function show(self: Self): f64;
}
impl Show for f64 {
    function show(self: Self): f64 { return self * 2.5; }
}
function run(s: dyn Show): f64 { return s.show(); }
function main(): i32 {
    let x: f64 = 4.0;
    print("v=" + run(x).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "v=10"))
}

func TestDynTraitPrimitiveString(t *testing.T) {
	src := `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for string {
    function show(self: Self): i32 { return self.len(); }
}
function run(s: dyn Show): i32 { return s.show(); }
function main(): i32 {
    let x: string = "hello";
    print("len=" + run(x).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "len=5"))
}

func TestDynTraitPrimitiveHeterogeneous(t *testing.T) {
	src := `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for string {
    function show(self: Self): i32 { return self.len(); }
}
impl Show for i32 {
    function show(self: Self): i32 { return self + 100; }
}
function main(): i32 {
    let xs: dyn Show[] = ["hi", 7, "world"];
    let total: i32 = 0;
    for x in xs {
        total = total + x.show();
    }
    print("total=" + total.to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "total=114"))
}

func TestDynTraitPrimitiveMethodArg(t *testing.T) {
	src := `import "std/i32";
trait Adder {
    function add(self: Self, n: i32): i32;
}
impl Adder for i32 {
    function add(self: Self, n: i32): i32 { return self + n; }
}
function run(a: dyn Adder, n: i32): i32 { return a.add(n); }
function main(): i32 {
    let x: i32 = 10;
    print("r=" + run(x, 32).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "r=42"))
}

func TestDynTraitPrimitiveStringMethodArg(t *testing.T) {
	src := `import "std/i32";
trait Joiner {
    function joined_len(self: Self, other: string): i32;
}
impl Joiner for string {
    function joined_len(self: Self, other: string): i32 { return self.len() + other.len(); }
}
function run(j: dyn Joiner, other: string): i32 { return j.joined_len(other); }
function main(): i32 {
    let x: string = "abc";
    print("n=" + run(x, "de").to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "n=5"))
}

func TestDowncastStructHitMiss(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
impl Shape for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
}
function describe(s: dyn Shape): string {
    let c: Option[Circle] = s as? Circle;
    match (c) {
        Some(x) => { return "circle r=" + x.r.to_string(); },
        None => {
            let r: Option[Rect] = s as? Rect;
            match (r) {
                Some(y) => { return "rect a=" + y.area().to_string(); },
                None => { return "other"; },
            }
        },
    }
}
function main(): i32 {
    let d: dyn Shape = Circle { r: 5 };
    print(describe(d));
    let e: dyn Shape = Rect { w: 3, h: 4 };
    print(describe(e));
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "circle r=5\nrect a=12"))
}

func TestDowncastHeterogeneousArrayCount(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
impl Shape for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
}
function main(): i32 {
    let shapes: dyn Shape[] = [Circle { r: 2 }, Rect { w: 3, h: 4 }, Circle { r: 1 }, Rect { w: 1, h: 1 }];
    let circle_r_sum: i32 = 0;
    let rects: i32 = 0;
    for s in shapes {
        let c: Option[Circle] = s as? Circle;
        match (c) {
            Some(x) => { circle_r_sum = circle_r_sum + x.r; },
            None => { rects = rects + 1; },
        }
    }
    print("circle_r_sum=" + circle_r_sum.to_string());
    print("rects=" + rects.to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "circle_r_sum=3\nrects=2"))
}

func TestDowncastEnumTarget(t *testing.T) {
	src := `import "std/i32";
trait Describe {
    function tag(self: Self): i32;
}
enum Box { Pair(i32, i32), Single(i32) }
struct Dot { n: i32 }
impl Describe for Box {
    function tag(self: Self): i32 {
        match (self) {
            Pair(a, b) => { return a + b; },
            Single(v) => { return v; },
        }
    }
}
impl Describe for Dot {
    function tag(self: Self): i32 { return 100 + self.n; }
}
function check(d: dyn Describe): i32 {
    let c: Option[Box] = d as? Box;
    match (c) {
        Some(x) => { return x.tag(); },
        None => { return -1; },
    }
}
function main(): i32 {
    let a: dyn Describe = Pair(3, 4);
    let b: dyn Describe = Dot { n: 7 };
    print("a=" + check(a).to_string());
    print("b=" + check(b).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "a=7\nb=-1"))
}

func TestDowncastOnlyTargetRooted(t *testing.T) {
	src := `import "std/i32";
trait Shape {
    function area(self: Self): i32;
}
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle {
    function area(self: Self): i32 { return self.r * self.r; }
}
impl Shape for Rect {
    function area(self: Self): i32 { return self.w * self.h; }
}
function main(): i32 {
    let d: dyn Shape = Circle { r: 5 };
    let r: Option[Rect] = d as? Rect;
    match (r) {
        Some(x) => { print("rect=" + x.area().to_string()); },
        None => { print("not a rect"); },
    }
    let c: Option[Circle] = d as? Circle;
    match (c) {
        Some(x) => { print("circle=" + x.area().to_string()); },
        None => { print("not a circle"); },
    }
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "not a rect\ncircle=25"))
}

func TestDynMultiTraitDispatch(t *testing.T) {
	src := `import "std/i32";
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }
function describe(d: dyn Show + Weigh): string {
    return d.show() + "=" + d.weight().to_string();
}
function main(): i32 {
    // order-insensitive: dyn Weigh + Show normalises to the same set/vtable
    let one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "apple=150"))
}

func TestDynMultiTraitHeterogeneousArray(t *testing.T) {
	src := `import "std/i32";
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
struct Brick { kg: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }
impl Show  for Brick { function show(self: Self): string { return "brick"; } }
impl Weigh for Brick { function weight(self: Self): i32 { return self.kg * 1000; } }
function describe(d: dyn Show + Weigh): string {
    return d.show() + "=" + d.weight().to_string();
}
function main(): i32 {
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
	backendsAgree(t, src, interpOracle(t, src, "apple=120\nbrick=2000\ntotal=2120"))
}

func TestDynThreeTraitMiddleSegment(t *testing.T) {
	src := `import "std/i32";
trait Aa { function a1(self: Self): i32; function a2(self: Self): i32; }
trait Bb { function b1(self: Self): i32; }
trait Cc { function c1(self: Self): i32; }
struct S { x: i32 }
impl Aa for S { function a1(self: Self): i32 { return self.x; } function a2(self: Self): i32 { return self.x + 1; } }
impl Bb for S { function b1(self: Self): i32 { return self.x * 10; } }
impl Cc for S { function c1(self: Self): i32 { return self.x * 100; } }
function sum(d: dyn Aa + Bb + Cc): i32 {
    return d.a1() + d.a2() + d.b1() + d.c1();
}
function main(): i32 {
    let d: dyn Cc + Aa + Bb = S { x: 3 };
    print("sum=" + sum(d).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "sum=337"))
}

func TestDowncastMultiTraitHitMiss(t *testing.T) {
	src := `import "std/i32";
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
struct Brick { kg: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }
impl Show  for Brick { function show(self: Self): string { return "brick"; } }
impl Weigh for Brick { function weight(self: Self): i32 { return self.kg * 1000; } }
function describe(d: dyn Show + Weigh): string {
    let a: Option[Apple] = d as? Apple;
    match (a) {
        Some(x) => { return "apple g=" + x.g.to_string(); },
        None => {
            let b: Option[Brick] = d as? Brick;
            match (b) {
                Some(y) => { return "brick kg=" + y.kg.to_string(); },
                None => { return "other"; },
            }
        },
    }
}
function main(): i32 {
    let one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    let two: dyn Show + Weigh = Brick { kg: 2 };
    print(describe(two));
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "apple g=150\nbrick kg=2"))
}

func TestDowncastMultiTraitOnlyTargetRooted(t *testing.T) {
	src := `import "std/i32";
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
struct Brick { kg: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }
impl Show  for Brick { function show(self: Self): string { return "brick"; } }
impl Weigh for Brick { function weight(self: Self): i32 { return self.kg * 1000; } }
function main(): i32 {
    // only Apple is ever coerced; Brick appears only as a downcast target
    let d: dyn Show + Weigh = Apple { g: 7 };
    let b: Option[Brick] = d as? Brick;
    match (b) {
        Some(x) => { print("brick=" + x.weight().to_string()); },
        None => { print("not a brick"); },
    }
    let a: Option[Apple] = d as? Apple;
    match (a) {
        Some(x) => { print("apple=" + x.show() + ":" + x.weight().to_string()); },
        None => { print("not an apple"); },
    }
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "not a brick\napple=apple:7"))
}

func TestDowncastThreeTrait(t *testing.T) {
	src := `import "std/i32";
trait Aa { function a1(self: Self): i32; }
trait Bb { function b1(self: Self): i32; }
trait Cc { function c1(self: Self): i32; }
struct S { x: i32 }
struct Tee { y: i32 }
impl Aa for S { function a1(self: Self): i32 { return self.x; } }
impl Bb for S { function b1(self: Self): i32 { return self.x * 10; } }
impl Cc for S { function c1(self: Self): i32 { return self.x * 100; } }
impl Aa for Tee { function a1(self: Self): i32 { return self.y; } }
impl Bb for Tee { function b1(self: Self): i32 { return self.y * 10; } }
impl Cc for Tee { function c1(self: Self): i32 { return self.y * 100; } }
function check(d: dyn Aa + Bb + Cc): i32 {
    let s: Option[S] = d as? S;
    match (s) {
        Some(v) => { return v.x; },
        None => {
            let tt: Option[Tee] = d as? Tee;
            match (tt) {
                Some(w) => { return -w.y; },
                None => { return 0; },
            }
        },
    }
}
function main(): i32 {
    let d: dyn Cc + Aa + Bb = S { x: 3 };
    let e: dyn Aa + Bb + Cc = Tee { y: 9 };
    print("d=" + check(d).to_string());
    print("e=" + check(e).to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "d=3\ne=-9"))
}

func TestDynTraitStdlibDisplay(t *testing.T) {
	src := `import "core/cmp";
import "std/i32";
function render(xs: dyn cmp.Display[]): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < xs.len()) {
        if (i > 0) { out = out + ", "; }
        out = out + xs[i].to_string();
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
	backendsAgree(t, src, interpOracle(t, src, "42, hi, true"))
}

func TestDynTraitStdlibDisplayByte(t *testing.T) {
	src := `import "core/cmp";
import "std/i32";
function main(): i32 {
    let s: string = "A";
    let b: u8 = s[0];
    let xs: dyn cmp.Display[] = [b, 7];
    print(xs[0].to_string() + "/" + xs[1].to_string());
    return 0;
}
`
	backendsAgree(t, src, interpOracle(t, src, "65/7"))
}
