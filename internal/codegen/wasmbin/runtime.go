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
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

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
// helper its ops will need. Today only OpStrLen → __lang_str_len.
func scanRuntimeHelpers(prog *ir.Program) runtimeNeeds {
	var needs runtimeNeeds
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpStrLen:
				needs.add("__lang_str_len")
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
}

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
