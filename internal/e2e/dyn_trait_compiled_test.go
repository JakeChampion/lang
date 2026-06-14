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
