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
