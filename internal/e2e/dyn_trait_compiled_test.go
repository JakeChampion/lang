// `dyn Trait` runtime dispatch on the wasm backend (slice 2b — see
// docs/DYN-TRAITS.md §4.2.1). A concrete struct value coerces to a
// `dyn Trait` fat pointer `[data, vtable]`; `d.m()` loads the method's
// table index from the vtable and call_indirect's through it. These
// tests compile + run real programs through wasmtime and assert the
// output matches the interpreter (the source of truth — it dispatches
// by the receiver's runtime type).
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynInterpStdout runs src on the interpreter and returns its trimmed
// stdout, failing the test on a non-zero exit.
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

// TestWASMDynTraitStructDispatch: a single struct receiver behind a
// `dyn Shape` local dispatches `area()` through its vtable. The wasm
// output must match the interpreter.
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
    var d: dyn Shape = Circle { r: 5 };
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

// TestWASMDynTraitHeterogeneousArray: a `dyn Shape[]` holding two
// different concrete types, iterated and dispatched in a loop. Each
// element's vtable routes to its own concrete impl. Exercises the
// two-trait-method case (area + a string-returning name) and the
// two-word array-element store/load fan-out.
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

// --- x86-64 boxed `dyn Trait` dispatch (slice 2c — docs/DYN-TRAITS.md
// §4.2.2). On natives a `dyn Trait` value is a BOXED one-word pointer to
// a heap `{data, vtable}` cell; the vtable is an array of absolute
// `__method_*` function pointers. These differential tests run the same
// sources the wasm tests use, through the native x86-64 backend, and
// assert the stdout matches the interpreter. ---

// TestX86_64DynTraitStructDispatch: single struct receiver behind a
// `dyn Shape` local, dispatched through its boxed vtable.
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
    var d: dyn Shape = Circle { r: 5 };
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

// TestX86_64DynTraitHeterogeneousArray: a `dyn Shape[]` holding two
// different concrete types, iterated + dispatched in a loop. Exercises
// the boxed one-word array-element store/load (single pointer stride),
// the two-trait-method vtable (area + a string-returning name), and a
// `dyn Shape` function parameter.
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

// --- arm64 boxed `dyn Trait` dispatch (slice 2d — docs/DYN-TRAITS.md
// §4.2.2). arm64 is also ptrW==8, so it uses the SAME boxed one-word
// representation as x86-64: a `dyn Trait` value is a single 8-byte
// pointer to a heap `{data, vtable}` cell; the vtable is a .rodata array
// of absolute `__method_*` function pointers. These differential tests
// run the same sources the x86-64 / wasm tests use, through the native
// arm64 backend under qemu-aarch64, and assert stdout matches the
// interpreter. ---

// TestArm64DynTraitStructDispatch: single struct receiver behind a
// `dyn Shape` local, dispatched through its boxed vtable.
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
    var d: dyn Shape = Circle { r: 5 };
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

// TestArm64DynTraitHeterogeneousArray: a `dyn Shape[]` holding two
// different concrete types, iterated + dispatched in a loop. Exercises
// the boxed one-word array-element store/load (single pointer stride),
// the two-trait-method vtable (area + a string-returning name), and a
// `dyn Shape` function parameter.
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

// --- `dyn Trait` over a PRIMITIVE receiver (i32) — regression for the
// x86-64 OpBoxDyn register-clobber bug + the first proven slice of
// "dyn over primitives". A primitive value isn't separately heap-
// allocated, so the boxed `{data, vtable}` cell is the program's FIRST
// allocation — which is exactly the case that triggers __fern_alloc's
// heap-init mmap `syscall`. x86-64's OpBoxDyn used to hold data/vtable
// in caller-save r10/r11 across that call; the syscall clobbered them
// (r11 ← RFLAGS), so the cell came back as garbage and dispatch
// segfaulted on the first `dyn` over an i32. Now data/vtable ride
// callee-saved rbx/r12 (the x86-64 mirror of arm64's x19/x20). wasm
// carries the i32 inline (no box) and arm64 already used callee-saved
// regs, so both already worked — these pin all three. ---

const dynPrimI32Src = `import "std/i32";
trait Show {
    function show(self: Self): i32;
}
impl Show for i32 {
    function show(self: Self): i32 { return self + 100; }
}
function run(s: dyn Show): i32 { return s.show(); }
function main(): i32 {
    var x: i32 = 5;
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

// --- Uniform primitive boxing for `dyn Trait` (docs/DYN-TRAITS.md §4.2.3).
// A primitive/string concrete coercing to `dyn` is heap-boxed so `data` is
// always a one-word pointer, and the vtable slots point at unboxing wrappers
// (`__dynbox_<C>_<m>`). The hard cases are i64/f64 on wasm (wider than the
// inline i32 data slot) and string on wasm + arm64 (a two-word value /
// non-integer receiver ABI). These differential tests run each across ALL
// THREE backends and assert the stdout matches the interpreter. ---

// dynAllBackends runs src on all three compiled backends + the interpreter
// and asserts each compiled stdout matches the interpreter's, and that the
// interpreter's matches wantInterp.
func dynAllBackends(t *testing.T, src, wantInterp string) {
	t.Helper()
	want := dynInterpStdout(t, src)
	if want != wantInterp {
		t.Fatalf("interp baseline = %q, want %q", want, wantInterp)
	}
	t.Run("wasm32-wasi", func(t *testing.T) {
		got := runWasmCapturingStdout(t, src)
		if got != want {
			t.Errorf("wasm = %q, want %q (interp)", got, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("x86-64 = %q, want %q (interp)", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("arm64 = %q, want %q (interp)", got, want)
		}
	})
}

// dynCompiledBackends runs src on all three compiled backends and asserts
// each stdout equals want. Unlike dynAllBackends it does NOT differential
// against the interpreter — used for `dyn` over i64, which the interpreter
// cannot dispatch (its width-less Number tags every integer "i32", a
// documented slice-1 limitation; see interp.valueTypeName / DYN-TRAITS.md
// §4.1). The compiled backends carry the width, so they dispatch correctly.
func dynCompiledBackends(t *testing.T, src, want string) {
	t.Helper()
	t.Run("wasm32-wasi", func(t *testing.T) {
		got := runWasmCapturingStdout(t, src)
		if got != want {
			t.Errorf("wasm = %q, want %q", got, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		got, code := compileAndRunX86_64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("x86-64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("x86-64 = %q, want %q", got, want)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		got, code := compileAndRunArm64(t, src)
		got = strings.TrimSpace(got)
		if code != 0 {
			t.Fatalf("arm64 exit = %d, want 0; stdout:\n%s", code, got)
		}
		if got != want {
			t.Errorf("arm64 = %q, want %q", got, want)
		}
	})
}

// dyn over i64 — the boxed `data` cell holds the full 8-byte value, which
// wasm's inline i32 data slot would truncate.
// Compiled-only: the interpreter can't dispatch dyn-over-i64 (width-less
// Number), so this checks the three backends against a literal expectation.
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
    var x: i64 = 9000000000 as i64;
    print("v=" + run(x).to_string());
    return 0;
}
`
	dynCompiledBackends(t, src, "v=9000001000")
}

// dyn over f64 — the boxed cell carries the 8-byte float and the wrapper
// reloads it with the f64 ABI. wasm is the backend that needs this.
func TestDynTraitPrimitiveF64(t *testing.T) {
	src := `import "std/float";
trait Show {
    function show(self: Self): f64;
}
impl Show for f64 {
    function show(self: Self): f64 { return self * 2.5; }
}
function run(s: dyn Show): f64 { return s.show(); }
function main(): i32 {
    var x: f64 = 4.0;
    print("v=" + run(x).to_string());
    return 0;
}
`
	dynAllBackends(t, src, "v=10")
}

// dyn over string — the value-box holds the two-word `(data, len)` string
// and the wrapper reloads it with the two-word load, which wasm + arm64
// both need. The impl returns `self.len()` (the design's example).
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
    var x: string = "hello";
    print("len=" + run(x).to_string());
    return 0;
}
`
	dynAllBackends(t, src, "len=5")
}

// Heterogeneous `dyn Show[]` mixing string + i32 concretes — each element's
// vtable routes to its own unboxing wrapper, proving the per-concrete box
// layout (two-word string vs one-word i32) and dispatch coexist in one array.
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
    var xs: dyn Show[] = ["hi", 7, "world"];
    var total: i32 = 0;
    for x in xs {
        total = total + x.show();
    }
    print("total=" + total.to_string());
    return 0;
}
`
	// "hi".len()=2, 7+100=107, "world".len()=5 → 114
	dynAllBackends(t, src, "total=114")
}

// A trait method WITH an argument on a primitive receiver — proves the
// wrapper passes method args straight through past the unboxed receiver.
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
    var x: i32 = 10;
    print("r=" + run(x, 32).to_string());
    return 0;
}
`
	dynAllBackends(t, src, "r=42")
}

// A trait method with a STRING argument on a string receiver — both the
// receiver (unboxed from the value cell) and the arg are two-word strings,
// proving the two-word arg ABI survives past the unboxed receiver.
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
    var x: string = "abc";
    print("n=" + run(x, "de").to_string());
    return 0;
}
`
	dynAllBackends(t, src, "n=5")
}

// --- `e as? T` fallible downcast codegen (docs/DYN-TRAITS.md §9).
// `e as? T` lowers to a vtable-pointer compare: extract the dyn value's
// vtable word (boxed-cell deref on natives, inline high-word on wasm),
// compare it against the static __vtable_<Trait>_<T> address
// (OpConstVtable), and build Some(data) on a hit / None on a miss. These
// differential tests run the compiled output (all three backends) against
// the interpreter (the source of truth — it downcasts by the receiver's
// runtime type tag). ---

// TestDowncastStructHitMiss: a `dyn Shape` holding a Circle. `s as? Circle`
// hits → Some, and the bound value is usable concretely (x.r); `s as? Rect`
// misses → None. Exercises both arms of the vtable compare on every backend.
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
    var c: Option[Circle] = s as? Circle;
    match (c) {
        Some(x) => { return "circle r=" + x.r.to_string(); },
        None => {
            var r: Option[Rect] = s as? Rect;
            match (r) {
                Some(y) => { return "rect a=" + y.area().to_string(); },
                None => { return "other"; },
            }
        },
    }
}
function main(): i32 {
    var d: dyn Shape = Circle { r: 5 };
    print(describe(d));
    var e: dyn Shape = Rect { w: 3, h: 4 };
    print(describe(e));
    return 0;
}
`
	dynAllBackends(t, src, "circle r=5\nrect a=12")
}

// TestDowncastHeterogeneousArrayCount: downcast each element of a
// heterogeneous `dyn Shape[]` to Circle, summing the radii of the hits and
// counting the misses. Proves the compare distinguishes concrete types
// per-element (each element's vtable word identifies its own type).
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
    var shapes: dyn Shape[] = [Circle { r: 2 }, Rect { w: 3, h: 4 }, Circle { r: 1 }, Rect { w: 1, h: 1 }];
    var circle_r_sum: i32 = 0;
    var rects: i32 = 0;
    for s in shapes {
        var c: Option[Circle] = s as? Circle;
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
	dynAllBackends(t, src, "circle_r_sum=3\nrects=2")
}

// TestDowncastEnumTarget: an enum concrete target. A payload-carrying enum
// dispatches (and downcasts) like any heap value; `d as? Box` hits when the
// dyn holds the enum, misses for a struct. (A payloadless-only enum behind
// dyn is a separate pre-existing dispatch limitation, not exercised here.)
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
    var c: Option[Box] = d as? Box;
    match (c) {
        Some(x) => { return x.tag(); },
        None => { return -1; },
    }
}
function main(): i32 {
    var a: dyn Describe = Pair(3, 4);
    var b: dyn Describe = Dot { n: 7 };
    print("a=" + check(a).to_string());
    print("b=" + check(b).to_string());
    return 0;
}
`
	dynAllBackends(t, src, "a=7\nb=-1")
}

// TestDowncastOnlyTargetRooted: a downcast target T (Rect) that is NEVER
// coerced to `dyn Shape` — only ever appears as a downcast target. Its
// __vtable_Shape_Rect (which the compare references) must still emit and
// its __method_Rect_* must survive tree-shake / IR dead-function
// elimination, or the build fails to link / find the func. Guards the
// DowncastImplMethods rooting (docs/DYN-TRAITS.md §9).
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
    var d: dyn Shape = Circle { r: 5 };
    var r: Option[Rect] = d as? Rect;
    match (r) {
        Some(x) => { print("rect=" + x.area().to_string()); },
        None => { print("not a rect"); },
    }
    var c: Option[Circle] = d as? Circle;
    match (c) {
        Some(x) => { print("circle=" + x.area().to_string()); },
        None => { print("not a circle"); },
    }
    return 0;
}
`
	dynAllBackends(t, src, "not a rect\ncircle=25")
}

// --- Multi-trait `dyn A + B` dispatch through the MERGED vtable
// (docs/DYN-TRAITS.md §10). A concrete C impl-ing both traits coerces to
// `dyn A + B`; calling a method from EACH trait dispatches through the
// concatenated (sorted-set, concrete) vtable. The differential against the
// interpreter (the source of truth) proves compiled == interp. ---

// TestDynMultiTraitDispatch: a single `dyn Show + Weigh` value calls a
// method from EACH trait (Show.show + Weigh.weight). Weigh.weight sits in
// the merged vtable AFTER Show's methods, so this exercises a non-zero
// global slot (the prefix offset), not just an append.
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
    var one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    return 0;
}
`
	dynAllBackends(t, src, "apple=150")
}

// TestDynMultiTraitHeterogeneousArray: a `dyn Show + Weigh[]` holding two
// different concrete types, iterated with BOTH-trait calls per element.
// Each element's merged vtable routes both traits' methods to its own
// concrete impls.
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
	dynAllBackends(t, src, "apple=120\nbrick=2000\ntotal=2120")
}

// TestDynThreeTraitMiddleSegment: a THREE-trait `dyn A + B + C` calling a
// method from each trait — including the MIDDLE trait B, whose method sits
// at a non-zero offset that is neither 0 (A) nor the tail (C). This proves
// the global-slot math (prefix = sum of earlier traits' method counts),
// not merely "first segment + appended last segment". A has 2 methods, so
// B.b1 is at global slot 2 and C.c1 at slot 3.
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
    var d: dyn Cc + Aa + Bb = S { x: 3 };
    print("sum=" + sum(d).to_string());
    return 0;
}
`
	// a1=3, a2=4, b1=30, c1=300 → 337
	dynAllBackends(t, src, "sum=337")
}

// --- Multi-trait `dyn A + B` DOWNCAST (`e as? T`) through the MERGED
// vtable (docs/DYN-TRAITS.md §10). emitDowncast keys OpConstVtable by the
// whole trait set (dynVtableSetKey), so the compare references the same
// `__vtable_<A+B>_<T>` cell a multi-trait coercion of T stores — exact for
// any trait set. Differential vs the interpreter (source of truth). ---

// TestDowncastMultiTraitHitMiss: a `dyn Show + Weigh` value downcast to the
// matching concrete (Some) and to a non-matching concrete that ALSO impls
// both traits (None). Proves the merged-vtable compare distinguishes the two
// concretes — the box was coerced with Apple's merged vtable, so `as? Apple`
// hits and `as? Brick` misses, and vice-versa.
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
    var a: Option[Apple] = d as? Apple;
    match (a) {
        Some(x) => { return "apple g=" + x.g.to_string(); },
        None => {
            var b: Option[Brick] = d as? Brick;
            match (b) {
                Some(y) => { return "brick kg=" + y.kg.to_string(); },
                None => { return "other"; },
            }
        },
    }
}
function main(): i32 {
    var one: dyn Weigh + Show = Apple { g: 150 };
    print(describe(one));
    var two: dyn Show + Weigh = Brick { kg: 2 };
    print(describe(two));
    return 0;
}
`
	dynAllBackends(t, src, "apple g=150\nbrick kg=2")
}

// TestDowncastMultiTraitOnlyTargetRooted: a multi-trait downcast target
// (Brick) that is NEVER coerced to `dyn Show + Weigh` — only ever a downcast
// target. The MERGED `__vtable_Show+Weigh_Brick` (which the compare
// references) must still emit and ALL of Brick's __method_* (both traits)
// must survive tree-shake, or the merged vtable cell references a dropped
// symbol. Guards the multi-trait DowncastImplMethods rooting (rooting every
// trait in the set, not just the primary).
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
    var d: dyn Show + Weigh = Apple { g: 7 };
    var b: Option[Brick] = d as? Brick;
    match (b) {
        Some(x) => { print("brick=" + x.weight().to_string()); },
        None => { print("not a brick"); },
    }
    var a: Option[Apple] = d as? Apple;
    match (a) {
        Some(x) => { print("apple=" + x.show() + ":" + x.weight().to_string()); },
        None => { print("not an apple"); },
    }
    return 0;
}
`
	dynAllBackends(t, src, "not a brick\napple=apple:7")
}

// TestDowncastThreeTrait: a THREE-trait `dyn A + B + C` downcast — the
// merged key has three segments; the downcast still keys the (set, T) vtable
// correctly. Hit on the matching concrete, miss on a different concrete that
// also impls all three.
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
    var s: Option[S] = d as? S;
    match (s) {
        Some(v) => { return v.x; },
        None => {
            var tt: Option[Tee] = d as? Tee;
            match (tt) {
                Some(w) => { return -w.y; },
                None => { return 0; },
            }
        },
    }
}
function main(): i32 {
    var d: dyn Cc + Aa + Bb = S { x: 3 };
    var e: dyn Aa + Bb + Cc = Tee { y: 9 };
    print("d=" + check(d).to_string());
    print("e=" + check(e).to_string());
    return 0;
}
`
	dynAllBackends(t, src, "d=3\ne=-9")
}

// `dyn` over the STDLIB `core/cmp.Display` — the trait every generic
// "printable" bound names, and the first `dyn` set whose implementor list
// is not written by the program under test. Two lowering gaps kept this
// interpreter-only:
//
//   - `core/cmp` carries `impl Display for u8` (the byte type has no scalar
//     module, so its Display impl has a real body). `u8` was absent from
//     astTypeForConcreteName, so the value-box layout lookup failed and the
//     whole `dyn cmp.Display` set was rejected before any code was emitted.
//   - collectVtables over-approximates — a vtable per IMPLEMENTOR, not per
//     coerced concrete. That is harmless for the vtables themselves (nothing
//     names them, so no backend emits them) but not for the unboxing wrappers
//     built from them: a wrapper for an implementor no site coerces is dead
//     code still calling `__method_<C>_<m>`, which tree-shake dropped. The
//     natives failed to link on the undefined label.
//
// Both are only reachable through a trait whose implementor set is wider
// than the program's coercion sites, which is exactly what a stdlib trait is.
func TestDynTraitStdlibDisplay(t *testing.T) {
	src := `import "core/cmp";
import "std/i32";
function render(xs: dyn cmp.Display[]): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < xs.len()) {
        if (i > 0) { out = out + ", "; }
        out = out + xs[i].to_string();
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
	dynAllBackends(t, src, "42, hi, true")
}

// A u8 element actually coerced into the `dyn cmp.Display` set, so the u8
// value-box layout is exercised rather than merely tolerated: the byte is
// boxed at the coercion site and read back through
// `__dynbox_u8_to_string`.
func TestDynTraitStdlibDisplayByte(t *testing.T) {
	src := `import "core/cmp";
import "std/i32";
function main(): i32 {
    var s: string = "A";
    var b: u8 = s[0];
    var xs: dyn cmp.Display[] = [b, 7];
    print(xs[0].to_string() + "/" + xs[1].to_string());
    return 0;
}
`
	dynAllBackends(t, src, "65/7")
}
