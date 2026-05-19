// Synthetic runtime-helper functions appended to the module after
// the user functions. These exist to implement IR ops (OpStrLen,
// later: OpAlloc / OpStrEq / OpStrConcat) without forcing every
// caller to inline the same code sequence.
//
// Each helper is gated by a usage scan over prog.Funcs — programs
// that never call OpStrLen pay zero bytes for the str_len helper.
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
type runtimeHelperSpec struct {
	params  []byte
	results []byte
	body    func() []byte
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
func buildAllocBody() []byte {
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

func buildStrLenBody() []byte {
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
