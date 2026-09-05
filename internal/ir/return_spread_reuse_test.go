// The RETURN-position struct update `return T { ...p, f: v }` is the
// state-threading shape every emitter in the self-host compiler is built out
// of — `s = s.emit(op)` calls one of these 2,340 times in irlower.fern alone,
// and each call allocated a fresh box, retained every carried field into it,
// and deep-dropped the receiver's box on the way out. p is an
// owned-by-default parameter there (the callee's exit sweep already frees it),
// so the frame may repurpose the box instead: computeReturnSpreadReuse admits
// the site and emitStructUpdateReuse writes the changed fields in place under
// the runtime is_unique gate.
package ir_test

import (
	"fmt"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// emitLike is the measured shape: a receiver method returning a spread of
// itself with one field replaced, threaded by the caller.
const emitLikeSrc = `struct St { ops: i32[], names: string[], ctrl: i32 }
function (s: St) emit(op: i32): St {
    var nctrl: i32 = s.ctrl;
    if (op == 1) { nctrl = s.ctrl + 1; }
    return St { ...s, ops: s.ops.append(op), ctrl: nctrl };
}
function main(): i32 {
    var s: St = St { ops: [], names: ["a"], ctrl: 0 };
    var i: i32 = 0;
    while (i < 5) { s = s.emit(i); i = i + 1; }
    return s.ops.len() + s.ctrl;
}`

func TestReturnSpreadReusesOwnedParamBox(t *testing.T) {
	ip := lowerForTest(t, emitLikeSrc)
	fn := funcByName(ip, "__method_St_emit")
	if fn == nil {
		t.Fatal("emit not lowered")
	}
	if got := allocReuseCount(fn); got != 1 {
		t.Errorf("return-spread of an owned receiver should reuse its box (one __alloc_reuse), got %d", got)
	}
	for _, op := range fn.Ops {
		if op.Kind == ir.OpAlloc {
			t.Errorf("the reuse replaces the fresh box, found an OpAlloc at %s", op.Pos)
		}
	}
}

// The carried fields are what the reuse buys: on the reuse branch `names` is
// left exactly as the box holds it, so its retain moves under the fresh-alloc
// guard instead of running on every call.
func TestReturnSpreadCarriedRetainIsGuarded(t *testing.T) {
	ip := lowerForTest(t, emitLikeSrc)
	fn := funcByName(ip, "__method_St_emit")
	incs := 0
	depth := 0
	guarded := 0
	for _, op := range fn.Ops {
		switch op.Kind {
		case ir.OpIf:
			depth++
		case ir.OpEnd:
			if depth > 0 {
				depth--
			}
		case ir.OpRcInc:
			incs++
			if depth > 0 {
				guarded++
			}
		}
	}
	if incs == 0 || incs != guarded {
		t.Errorf("every carried-field retain should sit under the fresh-alloc guard: %d of %d guarded", guarded, incs)
	}
}

// A function reached through a function VALUE borrows its parameters
// unconditionally (paramVerdictBorrowed, #7307): OpCallIndirect has no callee
// name, so the call site emits no retain for an argument it keeps — and rc==1
// inside the callee then means the CALLER's sole reference, not the frame's.
// Ownership is what admits the reuse, never the runtime count on its own.
func TestReturnSpreadRefusesAddressTakenCallee(t *testing.T) {
	ip := lowerForTest(t, `struct St { ops: i32[], ctrl: i32 }
function bump(s: St): St { return St { ...s, ctrl: s.ctrl + 1 }; }
function apply(f: (St) => St, s: St): St { return f(s); }
function main(): i32 {
    var s: St = St { ops: [1], ctrl: 0 };
    s = apply(bump, s);
    return s.ctrl + s.ops.len();
}`)
	if got := allocReuseCount(funcByName(ip, "bump")); got != 0 {
		t.Errorf("an address-taken callee borrows its param and must not reuse, got %d __alloc_reuse", got)
	}
}

// A `defer` runs AFTER the return value is built and can name p, so a function
// with one refuses — the reuse would hand the defer a box whose fields have
// already been overwritten, out of an emptied slot. The same body without the
// defer reuses, so the refusal is the defer's and not the shape's.
func TestReturnSpreadRefusesWithDefer(t *testing.T) {
	const body = `struct St { ops: i32[], ctrl: i32 }
function (s: St) bump(): St {
    %s
    return St { ...s, ctrl: s.ctrl + 1 };
}
function note(v: i32): i32 { return v; }
function main(): i32 {
    var s: St = St { ops: [1], ctrl: 0 };
    s = s.bump();
    return s.ctrl + s.ops.len();
}`
	withDefer := lowerForTest(t, fmt.Sprintf(body, "defer { note(s.ctrl); }"))
	if got := allocReuseCount(funcByName(withDefer, "__method_St_bump")); got != 0 {
		t.Errorf("a function with a defer must not reuse, got %d __alloc_reuse", got)
	}
	without := lowerForTest(t, fmt.Sprintf(body, ""))
	if got := allocReuseCount(funcByName(without, "__method_St_bump")); got != 1 {
		t.Errorf("the same body without the defer should reuse, got %d __alloc_reuse", got)
	}
}

// A REPLACED string field is not placeable (two words, per-ABI retain), so the
// site declines. The same struct reuses fine when the string is only CARRIED —
// TestReturnSpreadAdmitsCarriedString below.
func TestReturnSpreadRefusesReplacedStringField(t *testing.T) {
	ip := lowerForTest(t, `struct St { tag: string, ctrl: i32 }
function (s: St) rename(v: string): St { return St { ...s, tag: v }; }
function main(): i32 {
    var s: St = St { tag: "a", ctrl: 0 };
    s = s.rename("bb");
    return s.tag.len() + s.ctrl;
}`)
	if got := allocReuseCount(funcByName(ip, "__method_St_rename")); got != 0 {
		t.Errorf("a replaced string field must decline, got %d __alloc_reuse", got)
	}
}

// A carried string (and a bool) ride the reuse: the reuse branch never reads or
// writes them, so the two-word shape that keeps strings out of the placeable
// set does not arise. LowerState — the struct this issue is about — has one of
// each, so without this the measured shape would not qualify at all.
func TestReturnSpreadAdmitsCarriedString(t *testing.T) {
	ip := lowerForTest(t, `struct St { tag: string, ok: boolean, ctrl: i32 }
function (s: St) bump(): St { return St { ...s, ctrl: s.ctrl + 1 }; }
function main(): i32 {
    var s: St = St { tag: "a", ok: true, ctrl: 0 };
    s = s.bump();
    return s.tag.len() + s.ctrl;
}`)
	if got := allocReuseCount(funcByName(ip, "__method_St_bump")); got != 1 {
		t.Errorf("a carried string / bool should still reuse, got %d __alloc_reuse", got)
	}
}
