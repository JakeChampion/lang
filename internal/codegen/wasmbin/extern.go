// P4c composite-result marshalling for `@import` externs
// (docs/WIT-BRING-YOUR-OWN.md). P4b lowered scalar externs directly to a core
// import. A composite *result* uses the canonical-ABI return-area convention:
// the core import gains a trailing return-area pointer param and returns
// nothing; the host writes the result there, allocating any backing bytes via
// the module's exported cabi_realloc. The Fern call therefore can't resolve
// straight to the import — it resolves to a generated *wrapper* that allocates
// the return area, calls the raw import, and lifts the host bytes into a Fern
// value.
//
// This first slice covers a `string` (≡ `list<u8>`) result: at the canonical
// ABI both are `(ptr, len)`, so the wrapper reuses the existing
// __bytes_to_lang_string lift. Other composite results (arrays, records, …)
// stay rejected until their slices land.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// buildExternStringResultWrapper returns the body builder for the wrapper of a
// string/list<u8>-returning extern. nparams is the count of (scalar, one-slot)
// parameters; rawImport is the raw import's name in helperIdxs. The wrapper's
// type is (scalar params…) -> (i32 i32) — a Fern heap-form string pair.
func buildExternStringResultWrapper(nparams int, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		lift := idxs["__bytes_to_lang_string"]
		imp := idxs[rawImport]
		retbuf := uint32(nparams) // first local after the params

		var body []byte
		// retbuf = (__fern_alloc(12) + 3) & ~3 — the canonical return area
		// (data @+0, len @+4) must be 4-byte aligned, but __fern_alloc bumps
		// without aligning (the heap can sit right after odd-length string
		// data), so over-allocate by 3 bytes of slack and round up.
		body = inst.InstI32Const(body, 12)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 3)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -4)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, retbuf)
		// raw import: forward each scalar param, then the return-area pointer.
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, retbuf)
		body = inst.InstCall(body, imp)
		// lift: __bytes_to_lang_string(load(retbuf+0), load(retbuf+4)) — copies
		// the host bytes into a fresh Fern heap buffer and yields (data, len).
		body = inst.InstLocalGet(body, retbuf)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, retbuf)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstCall(body, lift)
		// The (data, len) pair is the wrapper's result.

		locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32) // retbuf
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternListU8ResultWrapper is the u8[] counterpart of the string-result
// wrapper: it lowers a `list<u8>` result the same way (canonical return area),
// then materializes a Fern `u8[]` array. A native array is length-prefixed —
// the value is a pointer to the elements with the count at `ptr-4` — so the
// wrapper allocates `4 + n`, stores the count, memory.copys the host bytes
// (u8 = 1-byte stride) just past it, and returns the element pointer. Wrapper
// type is (scalar params…) -> i32.
//
// Locals after the params: 0:$rb (return area) 1:$dp (host data) 2:$n (len)
// 3:$arr (array block base).
func buildExternListU8ResultWrapper(nparams int, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		dp := uint32(nparams + 1)
		n := uint32(nparams + 2)
		arr := uint32(nparams + 3)

		var body []byte
		// rb = (__fern_alloc(12) + 3) & ~3 — 4-byte aligned return area.
		body = inst.InstI32Const(body, 12)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 3)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -4)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// dp = load(rb+0); n = load(rb+4).
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, dp)
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, n)
		// arr = __fern_alloc(4 + n); store count at arr; copy bytes to arr+4.
		body = inst.InstI32Const(body, 4)
		body = inst.InstLocalGet(body, n)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, arr)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstLocalGet(body, n)
		body = memory.InstI32Store(body, 2, 0) // count @ arr+0
		// memory.copy(arr+4, dp, n)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, dp)
		body = inst.InstLocalGet(body, n)
		body = memory.InstMemoryCopy(body)
		// return arr + 4 (pointer to elements; count lives at ptr-4).
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternStringParamWrapper handles an extern with one or more `string`
// parameters and a scalar (or void) result (P4c). A WIT `string`/`list<u8>`
// parameter lowers to the canonical `(ptr, len)` of contiguous UTF-8 bytes,
// but a Fern string arrives as an SSO-encoded `(data, len)` pair whose data is
// not a raw pointer for inline strings. The wrapper normalizes each string
// param to a heap buffer (emitStrNormalize) before forwarding it, passing
// scalar params straight through, then calls the raw import; its scalar result
// (if any) is left on the stack.
//
// The wrapper's params mirror the flattened Fern signature (string → 2 i32
// slots); 3 i32 scratch locals (buf, byteLen, i), reused across string params,
// follow the param slots.
func buildExternStringParamWrapper(params []ast.Param, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		imp := idxs[rawImport]
		nSlots := uint32(0)
		for _, p := range params {
			if isStringType(p.Type) {
				nSlots += 2
			} else {
				nSlots++
			}
		}
		bufL, byteLenL, iL := nSlots, nSlots+1, nSlots+2

		var body []byte
		slot := uint32(0)
		for _, p := range params {
			if isStringType(p.Type) {
				// (data@slot, len@slot+1) → normalized (bufL, byteLenL).
				body = emitStrNormalize(body, idxs, slot, slot+1, bufL, byteLenL, iL)
				body = inst.InstLocalGet(body, bufL)
				body = inst.InstLocalGet(body, byteLenL)
				slot += 2
			} else {
				body = inst.InstLocalGet(body, slot)
				slot++
			}
		}
		body = inst.InstCall(body, imp)
		// A scalar result from the call is the wrapper's result.

		locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32) // buf, byteLen, i
		return inst.PutFunctionBody(nil, locals, body)
	}
}
