package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The struct self-update `a = Asm { ...a, code: le32(a.code, v) }` hands the
// callee a field the same statement overwrites, so nothing observes the old
// buffer through `a` afterwards and the #4873 grow bracket has nothing to
// contain. Bracketing it forced the callee's every push onto the copy path —
// the x86 assembler copied its whole code buffer per emitted byte, and the
// self-host driver's native build went from seconds to past CI's limit.
func TestStructSelfUpdateFieldArgNotBracketed(t *testing.T) {
	ip := lowerForTest(t, `struct Asm { code: i32[], n: i32 }
function le32(buf: i32[], v: i32): i32[] { buf = buf.append(v & 255); return buf; }
function emit(own a: Asm, v: i32): Asm { a = Asm { ...a, code: le32(a.code, v) }; return a; }
function main(): i32 { var a: Asm = Asm { code: [], n: 0 }; a = emit(a, 1); return a.code.len(); }`)
	emit := fnNamed(t, ip, "emit")
	// The bracket's inc immediately precedes the call it protects, on a
	// value loaded through the field path; nothing else in this body
	// retains right before le32.
	for i, op := range emit.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != "le32" {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-12; j-- {
			if emit.Ops[j].Kind == ir.OpRcInc {
				t.Fatalf("emit brackets `a.code` around le32 (rc_inc at op %d before the call at %d); the field is superseded by the update:\n%s", j, i, ip)
			}
			if emit.Ops[j].Kind == ir.OpCallDirect {
				break
			}
		}
	}
}

// The return-position twin: `return Asm { ...a, code: le32(a.code, v) }` is
// the same superseded field with no store to `a` at all — the value simply
// dies, so nothing can read the old buffer back through it. The assignment
// form was exempted and this one was not, which left every `return X86Asm {
// ...a, code: … }` in the x86 assembler bracketed: 724 GB copied compiling
// one driver, against 6.6 GB with the exemption, for byte-identical output.
//
// The frame must OWN the base, which is what the third case pins: a borrowed
// parameter's box outlives the call and its caller can still read the field,
// so that one keeps its bracket.
func TestStructReturnUpdateFieldArgNotBracketed(t *testing.T) {
	ip := lowerForTest(t, `struct Asm { code: i32[], n: i32 }
function le32(buf: i32[], v: i32): i32[] { buf = buf.append(v & 255); return buf; }
function emitOwn(own a: Asm, v: i32): Asm { return Asm { ...a, code: le32(a.code, v) }; }
function emitLocal(v: i32): Asm { var a: Asm = Asm { code: [], n: 0 }; return Asm { ...a, code: le32(a.code, v) }; }
function emitBorrowed(a: Asm, v: i32): Asm { return Asm { ...a, code: le32(a.code, v) }; }
function main(): i32 { var a: Asm = Asm { code: [], n: 0 }; a = emitOwn(a, 1); a = emitBorrowed(a, 2); a = emitLocal(3); return a.code.len(); }`)
	for _, fn := range []string{"emitOwn", "emitLocal"} {
		if n := incsBeforeCall(fnNamed(t, ip, fn), "le32"); n > 0 {
			t.Errorf("%s brackets `a.code` around le32 (%d rc_inc before the call); the field is superseded by the returned literal:\n%s", fn, n, ip)
		}
	}
	// Anti-vacuity: the borrowed base still needs the bracket, so a test
	// that passes because nothing is ever bracketed would fail here.
	if n := incsBeforeCall(fnNamed(t, ip, "emitBorrowed"), "le32"); n == 0 {
		t.Errorf("emitBorrowed does NOT bracket `a.code`, but its box outlives the call and the caller can read the field le32 grew in place:\n%s", ip)
	}
}

// incsBeforeCall counts the OpRcInc ops in the run immediately preceding the
// first direct call to `callee` — the bracket's retain sits there, after any
// earlier call.
func incsBeforeCall(fn *ir.Func, callee string) int {
	for i, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect || op.Str != callee {
			continue
		}
		n := 0
		for j := i - 1; j >= 0 && j >= i-12; j-- {
			if fn.Ops[j].Kind == ir.OpRcInc {
				n++
			}
			if fn.Ops[j].Kind == ir.OpCallDirect {
				break
			}
		}
		return n
	}
	return 0
}
