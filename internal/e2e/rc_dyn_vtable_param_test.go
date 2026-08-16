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

// TestDynVtableParamBoundedOwnedModel: the receiver is borrowed now, so the
// caller is its only reclaimer — the loop must still be bump-bounded under the
// owned model, i.e. borrowing did not turn the callee's free into a leak.
func TestDynVtableParamBoundedOwnedModel(t *testing.T) {
	src := func(n string) string {
		return `import "std/i32";
trait Shape { function area(self: Self): i32; }
struct Boxed { tag: string }
impl Shape for Boxed { function area(self: Self): i32 { return self.tag.len(); } }
function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var sum: i32 = 0;
    while (i < ` + n + `) {
        var d: dyn Shape = Boxed { tag: "a heap string owned by a vtable-dispatched receiver" };
        sum = sum + d.area();
        i = i + 1;
    }
    return (__heap_bump_bytes() as i32) - before;
}`
	}
	t.Run("x86_64", func(t *testing.T) {
		defer withBorrowInfer(false)()
		small := mustRunX86_64FreeOn(t, src("50"))
		large := mustRunX86_64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		defer withBorrowInfer(false)()
		small := mustRunArm64FreeOn(t, src("50"))
		large := mustRunArm64FreeOn(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		defer withBorrowInfer(false)()
		small := runWasm(t, src("50"))
		large := runWasm(t, src("5000"))
		if small != large {
			t.Errorf("bump growth should be bounded: n=50 -> %d, n=5000 -> %d", small, large)
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
