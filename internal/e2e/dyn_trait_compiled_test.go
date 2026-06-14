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
