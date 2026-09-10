package ir

import (
	"strings"
	"testing"
)

// A `dyn Trait` local that a closure captures and the ENCLOSING scope then
// reassigns is boxed into a shared cell — boxableCapture admits it through
// ast.IsPointerType — so the rebind lowers to emitBoxedCellStore's counted
// path, whose release ladder loads the superseded element with
// payloadLoadOpFor and hands it to dropStructField.
//
// On wasm those two disagreed. payloadLoadOpFor gives an inline `dyn` the
// two-word string fan-out, while dropStructField's own dyn arm is gated on
// dynRcSupported (false on wasm) and the generic dropFnNameFor branch below it
// appends the trailing OpDrop its drop fns need — but __drop_dyn_<set> returns
// void. The emitted module failed wasm validation outright: "not enough
// arguments on the stack for drop". #8797; the verifier's own blindness to this
// shape is #8798, which is why the Coverage.Skipped assertion below is load-bearing.
const dynBoxedCaptureSrc = `
trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
struct Ci { r: i32 }
impl Shape for Ci { function area(self: Self): i32 { return self.r * 3i32; } }

function main(): i32 {
    var d: dyn Shape = Sq { s: 3i32 };
    var f: () => i32 = (() => d.area());
    d = Ci { r: 5i32 };
    return f();
}
`

func TestBoxedDynCaptureStoreBalancesOnWasm(t *testing.T) {
	p := lowerSource(t, dynBoxedCaptureSrc) // ptrW=4: wasm
	problems, cov, _ := Verify(p)

	// Non-vacuity: the verifier reports nothing for a function it abandoned,
	// so a skipped main would make this gate pass with the fix reverted.
	if reason, skipped := cov.Skipped["main"]; skipped {
		t.Fatalf("main was not modelled by the stack verifier (%s), so this gate proves nothing", reason)
	}

	for _, pr := range problems {
		t.Errorf("stack/rc problem: %s", pr.Error())
	}
}

// The counted store must be the shape under test: if the boxed-cell rebind
// ever stops reaching emitBoxedCellStore, the balance gate above still passes
// while proving nothing about it.
func TestBoxedDynCaptureReachesTheCountedStore(t *testing.T) {
	p := lowerSource(t, dynBoxedCaptureSrc)
	var main *Func
	for _, f := range p.Funcs {
		if f.Name == "main" {
			main = f
		}
	}
	if main == nil {
		t.Fatal("no main in lowered program")
	}
	// The two-word discard the wasm `dyn` release ladder ends in.
	for _, op := range main.Ops {
		if op.Kind == OpDrop && op.Width == WidthString {
			return
		}
	}
	var sb strings.Builder
	for _, op := range main.Ops {
		sb.WriteString(op.Kind.String())
		sb.WriteByte('\n')
	}
	t.Errorf("no two-word OpDrop in main: the dyn cell rebind no longer takes the counted store's release ladder\nops:\n%s", sb.String())
}

// The rebind's store must write BOTH words of the inline wasm `dyn`. The
// index-assign path used to pick its store width by hand, with no `dyn`
// case, so the cell store was a one-word store that left the other word on
// the operand stack: the closure read the superseded Sq (9, not 15) and the
// CLI's module failed validation (#8797's program).
func TestBoxedDynCaptureStoreIsTwoWordOnWasm(t *testing.T) {
	p := lowerSource(t, dynBoxedCaptureSrc)
	var main *Func
	for _, f := range p.Funcs {
		if f.Name == "main" {
			main = f
		}
	}
	if main == nil {
		t.Fatal("no main in lowered program")
	}
	for _, op := range main.Ops {
		if op.Kind == OpStore && op.Width == WidthString {
			return
		}
	}
	t.Errorf("no two-word OpStore in main: the dyn cell rebind stores one word of a two-word value")
}
