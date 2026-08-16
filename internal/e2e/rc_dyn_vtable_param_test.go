// Ownership of a vtable-dispatched method's parameters (#6465).
//
// `OpCallDyn` jumps through a function pointer read out of the vtable, so the
// call site has no callee NAME: `calleeParamOwnedByDefault` is never consulted
// and no caller-side retain is emitted. Under the owned model the impl method's
// exit nevertheless ran an `is_unique`-gated `__fern_box_free` on each
// owned-by-default param, freeing values nobody had retained — the caller's
// receiver, and any struct argument passed alongside it.
//
// `paramVerdict` now returns borrowed for every parameter of a vtable-listed
// method, so the two models agree. These cases pin that: the same programs
// under BOTH `ast.BorrowInferEnabled` settings, on all three backends.
package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// dynVtableParamSrc dispatches through the vtable twice per iteration: once on
// the receiver alone, once passing a second owned-by-default struct that lives
// across the loop. Per iteration 9 + 15 + 5 = 29.
const dynVtableParamSrc = `trait Shape {
    function area(self: Self): i32;
    function scaled(self: Self, by: Factor): i32;
}
struct Factor { k: i32 }
struct Square { side: i32 }
impl Shape for Square {
    function area(self: Self): i32 { return self.side * self.side; }
    function scaled(self: Self, by: Factor): i32 { return self.side * by.k; }
}
function main(): i32 {
    var f: Factor = Factor { k: 5 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var d: dyn Shape = Square { side: 3 };
        acc = acc + d.area() + d.scaled(f) + f.k;
        i = i + 1;
    }
    return (acc - 116) + __rc_underflow_count();
}`

// TestDynVtableParamOwnership runs the dispatch under both ownership models.
// Pre-fix the owned model (BorrowInferEnabled false) freed the receiver inside
// the callee and the caller freed it again: `FERN_SANITIZE=1` reported a
// use-after-free on both natives and wasm trapped with "pointer not aligned"
// once the corrupted freelist handed back a misaligned block.
func TestDynVtableParamOwnership(t *testing.T) {
	for _, borrow := range []bool{true, false} {
		name := "borrow_infer_on"
		if !borrow {
			name = "owned_model"
		}
		t.Run("x86_64/"+name, func(t *testing.T) {
			defer withBorrowInfer(borrow)()
			if _, code := compileAndRunX86_64FreeOn(t, dynVtableParamSrc); code != 0 {
				t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
			}
		})
		t.Run("arm64/"+name, func(t *testing.T) {
			defer withBorrowInfer(borrow)()
			if _, code := compileAndRunArm64FreeOn(t, dynVtableParamSrc); code != 0 {
				t.Errorf("got exit %d, want 0 (wrong value or rc over-release)", code)
			}
		})
		t.Run("wasm/"+name, func(t *testing.T) {
			defer withBorrowInfer(borrow)()
			if got := runWasm(t, dynVtableParamSrc); got != 0 {
				t.Errorf("got %d, want 0 (wrong value or rc over-release)", got)
			}
		})
	}
}

// dynVtableParamBumpSrc reports a VERDICT — 0 when a churn four times as long
// adds no fresh high-water, 1 when it grows. See dynAssignOverwriteBumpSrc for
// why the verdict rather than the byte count (main()'s value is an exit code,
// so a byte count is read modulo 256).
func dynVtableParamBumpSrc(n, wider string) string {
	churn := func(bound string) string {
		return `    while (i < ` + bound + `) {
        var d: dyn Shape = Boxed { tag: "a heap string owned by a vtable-dispatched receiver" };
        sum = sum + d.area();
        i = i + 1;
    }
`
	}
	return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
function main(): i32 {
    var sum: i32 = 0;
    var i: i32 = 0;
    var base: i32 = (__heap_bump_bytes() as i32);
` + churn(n) + `    var first: i32 = (__heap_bump_bytes() as i32) - base;
    var mid: i32 = (__heap_bump_bytes() as i32);
    i = 0;
` + churn(wider) + `    var second: i32 = (__heap_bump_bytes() as i32) - mid;
    if (second > first) { return 1; }
    return sum - sum;
}`
}

// TestDynVtableParamBoundedOwnedModel: the receiver is borrowed now, so the
// caller is its only reclaimer — the loop must still be bump-bounded under the
// owned model, i.e. borrowing did not turn the callee's free into a leak.
func TestDynVtableParamBoundedOwnedModel(t *testing.T) {
	src := dynVtableParamBumpSrc("500", "2000")
	t.Run("x86_64", func(t *testing.T) {
		defer withBorrowInfer(false)()
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		defer withBorrowInfer(false)()
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", code)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		defer withBorrowInfer(false)()
		if got := runWasm(t, src); got != 0 {
			t.Errorf("heap high-water grew with the churn length (verdict %d, want 0)", got)
		}
	})
}

// withBorrowInfer sets ast.BorrowInferEnabled for the duration of a sub-test
// and returns the restore func.
func withBorrowInfer(v bool) func() {
	prev := ast.BorrowInferEnabled
	ast.BorrowInferEnabled = v
	return func() { ast.BorrowInferEnabled = prev }
}
