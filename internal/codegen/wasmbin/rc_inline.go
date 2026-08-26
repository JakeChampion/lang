package wasmbin

import (
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// #4402 opt 2b, wasm leg: OpRcInc / OpRcDec / OpRcIsUnique lower to the
// helper's body spliced into the caller instead of a `call`. Cranelift does
// not inline across wasm functions, so every rc op was a real call frame —
// and rc ops are the densest instruction class in refcounted inner loops.
//
// Each sequence mirrors its runtime helper (buildRcIncBody, buildRcDecBody,
// buildRcIsUniqueBody in runtime.go) guard for guard. The helpers stay
// emitted: other runtime bodies call them, and a function over
// rcInlineMaxOps still routes through them.
//
// The one structural difference is control flow. The helpers short-circuit
// with `return`, which an inlined body cannot use; the guards are re-expressed
// as nested `if` blocks over the NEGATED conditions. The null test folds into
// the low-address test — rcLowAddrGuard is well above zero, so one unsigned
// `>=` covers both.
//
// scratch is the base of the three i32 locals reserved by fnInlinesRcOps:
// +0 the pointer, +1 the rc-word address, +2 the rc word.
func emitInlineRcOp(body []byte, kind ir.OpKind, scratch uint32) []byte {
	p, addr, rc := scratch, scratch+1, scratch+2
	if kind == ir.OpRcIsUnique {
		return emitInlineRcIsUnique(body, p, rc)
	}
	// ptr into scratch; the ops are pass-through, so it is pushed again
	// at the end whichever branch ran.
	body = inst.InstLocalSet(body, p)
	body = inst.InstLocalGet(body, p)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// addr = ptr - 8; rc = mem[addr].
		body = inst.InstLocalGet(body, p)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalTee(body, addr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, rc)
		// Static sentinel (high bit set) → leave the rc word alone.
		body = inst.InstI32Const(body, int32(-0x80000000))
		body = numeric.InstI32And(body)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			if kind == ir.OpRcDec {
				body = emitInlineRcUnderflowTally(body, rc)
			}
			body = inst.InstLocalGet(body, addr)
			body = inst.InstLocalGet(body, rc)
			body = inst.InstI32Const(body, 1)
			if kind == ir.OpRcInc {
				body = numeric.InstI32Add(body)
			} else {
				body = numeric.InstI32Sub(body)
			}
			body = memory.InstI32Store(body, 2, 0)
		}
		body = inst.InstEnd(body)
	}
	body = inst.InstEnd(body)
	return inst.InstLocalGet(body, p)
}

// emitInlineRcUnderflowTally is buildRcDecBody's over-release detector: past
// the null / low-address / sentinel guards an rc <= 0 is a genuine heap value
// being released once too often, so bump the counter at rcUnderflowAddr. The
// dec still happens afterwards, exactly as the helper does it.
func emitInlineRcUnderflowTally(body []byte, rc uint32) []byte {
	body = inst.InstLocalGet(body, rc)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LeS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, rcUnderflowAddr)
	body = inst.InstI32Const(body, rcUnderflowAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	return inst.InstEnd(body)
}

// emitInlineRcIsUnique is buildRcIsUniqueBody inline: 1 iff the pointer is a
// real, non-sentinel heap value whose rc is exactly 1, else 0. The result is
// the block's value rather than a scratch local, so the two `if`s are typed.
func emitInlineRcIsUnique(body []byte, p, rc uint32) []byte {
	body = inst.InstLocalSet(body, p)
	body = inst.InstLocalGet(body, p)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		body = inst.InstLocalGet(body, p)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, rc)
		body = inst.InstI32Const(body, int32(-0x80000000))
		body = numeric.InstI32And(body)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, encode.ValtypeI32)
		{
			body = inst.InstLocalGet(body, rc)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Eq(body)
		}
		body = inst.InstElse(body)
		body = inst.InstI32Const(body, 0) // static sentinel → not unique
		body = inst.InstEnd(body)
	}
	body = inst.InstElse(body)
	body = inst.InstI32Const(body, 0) // null / below the guard → not unique
	return inst.InstEnd(body)
}
