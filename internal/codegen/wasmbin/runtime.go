// Synthetic runtime-helper functions appended to the module after
// the user functions. These exist to implement IR ops (OpAlloc,
// OpStrLen, OpStrEq, OpStrConcat, OpStrLen-byte, the __lang_print
// WASI wrapper, etc.) without forcing every caller to inline the
// same code sequence.
//
// Each helper is gated by a usage scan over prog.Funcs — programs
// that never need a helper pay zero bytes for its body.
// runtimeHelperSpecs keeps the names + bodies + signatures in one
// place so adding a new helper is one entry.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// memInst* short aliases keep the buildAllocBody assembly readable
// without each line repeating the package qualifier. Alignment 2
// (log2 of 4 bytes) for i32 ops; offset 0 means the cursor address
// is the literal i32 on top of the stack at call time.
func memInstI32Load(buf []byte) []byte    { return memory.InstI32Load(buf, 2, 0) }
func memInstI32Store(buf []byte) []byte   { return memory.InstI32Store(buf, 2, 0) }
func memInstMemorySize(buf []byte) []byte { return memory.InstMemorySize(buf) }
func memInstMemoryGrow(buf []byte) []byte { return memory.InstMemoryGrow(buf) }

// runtimeHelperSpec describes one helper function's signature +
// pre-built body. Bodies are produced lazily (the `body` closure)
// so the heavy hand-crafted byte sequences only run when the
// helper is actually used.
//
// Bodies that call sibling helpers (e.g. __str_eq → __lang_str_len
// + __lang_str_byte) receive a name → funcidx map so the call
// targets are resolved at module-assembly time without any
// post-emission patching.
type runtimeHelperSpec struct {
	params  []byte
	results []byte
	body    func(helperIdxs map[string]uint32) []byte
}

// runtimeNeeds is the set of helpers a single Emit call needs,
// in stable order so the funcidx assignment is deterministic.
type runtimeNeeds struct {
	order []string // names in declaration order
	set   map[string]bool
}

func (r *runtimeNeeds) add(name string) {
	if r.set == nil {
		r.set = map[string]bool{}
	}
	if r.set[name] {
		return
	}
	r.set[name] = true
	r.order = append(r.order, name)
}

// scanRuntimeHelpers walks the IR program and records every
// helper its ops will need. Each entry here corresponds to one
// helper in runtimeHelperSpecs.
func scanRuntimeHelpers(prog *ir.Program) runtimeNeeds {
	var needs runtimeNeeds
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpStrLen:
				needs.add("__lang_str_len")
			case ir.OpAlloc:
				needs.add("__lang_alloc")
			case ir.OpMakeClosure, ir.OpMakeEnv:
				// Both ops call __lang_alloc to materialise
				// the env block; OpMakeClosure also allocs
				// a second 8-byte pair cell.
				needs.add("__lang_alloc")
			case ir.OpCallDirect:
				// Source-language built-ins lower to OpCallDirect
				// with the source name; the call-site lookup
				// goes through callDirectAlias which routes to
				// the synthetic helper. The trigger here uses
				// the same alias so the helper actually exists.
				switch callDirectAlias(op.Str) {
				case "__lang_print":
					// fd_write under the hood; transitively
					// pulls in the byte-copy + alloc helpers.
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_alloc")
					needs.add("__lang_print")
				case "__lang_exit":
					// wasi_proc_exit under the hood; nothing
					// else needed.
					needs.add("__lang_exit")
				case "__lang_random_i32":
					// wasi_random_get under the hood; writes
					// 4 random bytes to the fixed scratch slot
					// and returns them as an i32.
					needs.add("__lang_random_i32")
				case "__lang_now_ns":
					// wasi_clock_time_get + alloc-per-call for
					// the 8-byte output buffer.
					needs.add("__lang_alloc")
					needs.add("__lang_now_ns")
				}
			case ir.OpStrEq:
				// __str_eq's inline-side byte reads route
				// through __lang_str_byte, and the length
				// dispatch uses __lang_str_len.
				needs.add("__lang_str_len")
				needs.add("__lang_str_byte")
				needs.add("__str_eq")
			case ir.OpStrConcat:
				// __str_concat allocates a buffer sized by
				// the sum of the two operand lengths, then
				// copies bytes one-at-a-time via the SSO-
				// aware byte fetch. Returns the new (data,
				// len) pair as a heap-form string.
				needs.add("__lang_str_len")
				needs.add("__lang_str_byte")
				needs.add("__lang_alloc")
				needs.add("__str_concat")
			}
		}
	}
	return needs
}

// runtimeHelperSpecs is the registry. Keyed by the canonical
// helper name; the entry's body() builds the wasm bytes lazily.
var runtimeHelperSpecs = map[string]runtimeHelperSpec{
	"__lang_str_len": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32}, // (data, len)
		results: []byte{encode.ValtypeI32},
		body:    buildStrLenBody,
	},
	"__lang_alloc": {
		params:  []byte{encode.ValtypeI32}, // size
		results: []byte{encode.ValtypeI32}, // pointer
		body:    buildAllocBody,
	},
	"__lang_str_byte": {
		// (data, len, i) → i32 byte; inline-or-heap aware.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrByteBody,
	},
	"__lang_print": {
		// (data, len) → ()
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildPrintBody,
	},
	"__lang_exit": {
		// (code) → () — never returns, but the wasm signature
		// still has a void result.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildExitBody,
	},
	"__lang_random_i32": {
		// () → i32 — host-supplied random word via wasi_random_get.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildRandomI32Body,
	},
	"__lang_now_ns": {
		// () → i64 — nanoseconds since unix epoch from the
		// realtime clock via wasi_clock_time_get.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildNowNsBody,
	},
	"__str_eq": {
		// (a_data, a_len, b_data, b_len) → i32 (0 or 1).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrEqBody,
	},
	"__str_concat": {
		// (a_data, a_len, b_data, b_len) → (data, len). Multi-
		// value return for the two-word ABI.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrConcatBody,
	},
}

// allocCursorAddr is the memory offset where the bump cursor lives.
// Matches the WAT path's choice of 40. The cursor is 4 bytes;
// memory[40..44] holds the i32 LE pointer to the next free byte.
const allocCursorAddr = 40

// allocMinStart is the minimum value the bump cursor can take —
// must be past the cursor cell itself and any other reserved low-
// memory state. 64 matches the WAT path's floor.
const allocMinStart = 64

// buildStrLenBody assembles the wasm bytes for __lang_str_len.
//
// Signature: (param $data i32) (param $len i32) (result i32)
//
// SSO seam: the top bit of $len discriminates inline (1) vs
// heap (0) form.
//   - inline: byte length lives in bits 24..26 of $len (0..7).
//   - heap:   $len is the byte length directly.
//
// Body (logical):
//
//	if ($len & 0x80000000) != 0 {
//	    ($len >> 24) & 0x7
//	} else {
//	    $len
//	}
//
// Body (wasm):
//
//	local.get 1
//	i32.const 0x80000000
//	i32.and
//	if (result i32)
//	    local.get 1
//	    i32.const 24
//	    i32.shr_u
//	    i32.const 0x7
//	    i32.and
//	else
//	    local.get 1
//	end
// buildAllocBody assembles the wasm bytes for __lang_alloc.
//
// Signature: (param $size i32) (result i32)
//
// Body: bump cursor at memory[40]. Returns the OLD cursor, bumps
// to (cursor + size), and grows memory if the new end exceeds
// current size.
//
// Logical:
//
//	ptr  = mem[40]
//	end  = ptr + size
//	need = ((end + 65535) >> 16) - memory.size
//	if need > 0 { memory.grow(need); drop }
//	mem[40] = end
//	return ptr
//
// Wasm locals (in order):
//
//	0: $size  (param)
//	1: $ptr
//	2: $end
//	3: $need
func buildAllocBody(_ map[string]uint32) []byte {
	var body []byte
	// ptr = mem[40]
	body = inst.InstI32Const(body, allocCursorAddr)
	body = memInstI32Load(body)
	body = inst.InstLocalSet(body, 1) // $ptr
	// end = ptr + size
	body = inst.InstLocalGet(body, 1) // $ptr
	body = inst.InstLocalGet(body, 0) // $size
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 2) // $end
	// need = ((end + 65535) >> 16) - memory.size
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 65535)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32ShrU(body)
	body = memInstMemorySize(body)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalSet(body, 3) // $need
	// if need > 0 { memory.grow(need); drop }
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32GtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 3)
	body = memInstMemoryGrow(body)
	body = inst.InstDrop(body)
	body = inst.InstEnd(body)
	// mem[40] = end
	body = inst.InstI32Const(body, allocCursorAddr)
	body = inst.InstLocalGet(body, 2) // $end
	body = memInstI32Store(body)
	// return ptr
	body = inst.InstLocalGet(body, 1) // $ptr
	// Locals declaration: three i32 scratch slots (ptr, end, need)
	// after the single i32 param.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrByteBody assembles wasm bytes for __lang_str_byte.
//
// Signature: (param $data i32) (param $len i32) (param $i i32) (result i32)
//
// Logical:
//
//	if ($len & 0x80000000) != 0 {          // inline form
//	    if $i < 4 { ($data >> ($i*8)) & 0xff }
//	    else      { ($len  >> (($i-4)*8)) & 0xff }
//	} else {                                // heap form
//	    i32.load8_u at ($data + $i)
//	}
func buildStrByteBody(_ map[string]uint32) []byte {
	var body []byte
	// inline-vs-heap dispatch
	body = inst.InstLocalGet(body, 1) // $len
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		// inline branch
		body = inst.InstLocalGet(body, 2) // $i
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32LtU(body)
		body = inst.InstIfStart(body, encode.ValtypeI32)
		{
			// ($data >> ($i * 8)) & 0xff
			body = inst.InstLocalGet(body, 0) // $data
			body = inst.InstLocalGet(body, 2) // $i
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 0xff)
			body = numeric.InstI32And(body)
		}
		body = inst.InstElse(body)
		{
			// ($len >> (($i - 4) * 8)) & 0xff
			body = inst.InstLocalGet(body, 1) // $len
			body = inst.InstLocalGet(body, 2) // $i
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Sub(body)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 0xff)
			body = numeric.InstI32And(body)
		}
		body = inst.InstEnd(body)
	}
	body = inst.InstElse(body)
	{
		// heap branch: i32.load8_u at ($data + $i)
		body = inst.InstLocalGet(body, 0) // $data
		body = inst.InstLocalGet(body, 2) // $i
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
	}
	body = inst.InstEnd(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStrEqBody assembles wasm bytes for __str_eq.
//
// Signature: (param $a_data $a_len $b_data $b_len i32) (result i32)
// Locals (after params): $la (4), $lb (5), $i (6).
//
// Strategy:
//  1. Two-word pair equality fast path — identical (data, len)
//     pairs → equal. Catches both heap (same pointer + same len)
//     and inline (same bit-pattern) coincidences.
//  2. If pair-eq failed and BOTH operands have the inline flag
//     set, they must differ (inline encoding is deterministic).
//  3. Otherwise compare lengths via __lang_str_len. Different
//     lengths → not equal.
//  4. Byte loop via __lang_str_byte (handles inline + heap on
//     both sides transparently).
func buildStrEqBody(idxs map[string]uint32) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	var body []byte
	// Step 1: pair-eq fast path.
	body = inst.InstLocalGet(body, 0) // a_data
	body = inst.InstLocalGet(body, 2) // b_data
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, 1) // a_len
	body = inst.InstLocalGet(body, 3) // b_len
	body = numeric.InstI32Eq(body)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 2: both-inline distinct → return 0.
	body = inst.InstLocalGet(body, 1) // a_len
	body = inst.InstLocalGet(body, 3) // b_len
	body = numeric.InstI32And(body)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 3: la = __lang_str_len(a); lb = __lang_str_len(b); if differ return 0.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4) // $la
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Ne(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 4: byte loop.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $i = 0
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= $la: return 1.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 1)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		// if __str_byte(a, i) != __str_byte(b, i): return 0.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstCall(body, strByte)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstCall(body, strByte)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		// $i = $i + 1; continue loop.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	// Loop never falls through (every iteration ends in return
	// or br 0), but wasm validation still wants a terminating
	// instruction with the function's result type. `unreachable`
	// satisfies the verifier without emitting a runtime const.
	body = inst.InstUnreachable(body)

	// Three i32 locals: $la, $lb, $i.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrConcatBody assembles wasm bytes for __str_concat.
//
// Signature: (param $a_data $a_len $b_data $b_len i32) (result i32 i32)
// Locals (after params): $la (4), $lb (5), $dst (6), $i (7).
//
// Logical:
//
//	la  = __lang_str_len(a)
//	lb  = __lang_str_len(b)
//	dst = __lang_alloc(la + lb)
//	for i in 0..la: mem[dst+i]     = __lang_str_byte(a, i)
//	for i in 0..lb: mem[dst+la+i]  = __lang_str_byte(b, i)
//	return (dst, la + lb)
//
// Result is heap-form (top bit of len clear) regardless of input
// forms; the bytes always land in memory at `dst`.
func buildStrConcatBody(idxs map[string]uint32) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	alloc := idxs["__lang_alloc"]
	var body []byte
	// la = __lang_str_len(a)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4) // $la
	// lb = __lang_str_len(b)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	// dst = __lang_alloc(la + lb)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6) // $dst
	// Loop 1: i in 0..la — copy a's bytes into mem[dst + i].
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= $la: break (br to enclosing block, label 1).
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// mem[dst + i] = __lang_str_byte(a, i)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i = $i + 1; continue loop.
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// Loop 2: i in 0..lb — copy b's bytes into mem[dst + la + i].
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// addr = dst + la + i
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		// byte = __lang_str_byte(b, i)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i++
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// Return (dst, la + lb) as the multi-value result.
	body = inst.InstLocalGet(body, 6) // dst (data)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body) // total (len)
	// Four i32 locals: $la, $lb, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

func buildStrLenBody(_ map[string]uint32) []byte {
	var body []byte
	// $len is wasm local 1; $data is local 0 (unused for length).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000)) // 0x80000000 as signed
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 24)
	body = numeric.InstI32ShrU(body)
	body = inst.InstI32Const(body, 0x7)
	body = numeric.InstI32And(body)
	body = inst.InstElse(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstEnd(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}
