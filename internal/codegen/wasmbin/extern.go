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

// buildExportStringResultWrapper builds the core wrapper for a string-returning
// `@export` function (P6 — docs/WIT-BRING-YOUR-OWN.md). The Fern function
// compiles to a core func returning the two-word `(data, len)` pair; the
// canonical ABI for a `func(...) -> string` export instead returns a single i32
// pointer to a `[data, len]` return area (the memory lift reads it). The
// wrapper forwards the scalar params, calls the user func, normalizes the
// returned string, and writes the canonical 4-byte-aligned return area.
//
// A Fern string is SSO-encoded — short strings pack their bytes inline into the
// (data, len) words, so the words are NOT a raw (ptr, byte_len). The wrapper
// normalizes the pair into a heap buffer (emitStrNormalize, the seam the
// string-param wrapper uses) before writing the [ptr, len] return area.
//
// Locals after the params: 0:$data 1:$len 2:$buf 3:$byteLen 4:$i 5:$ra (i32).
func buildExportStringResultWrapper(idxs map[string]uint32, userFuncIdx uint32, nparams int) (body []byte, locals []byte) {
	data := uint32(nparams)
	lenL := uint32(nparams + 1)
	buf := uint32(nparams + 2)
	byteLen := uint32(nparams + 3)
	iL := uint32(nparams + 4)
	ra := uint32(nparams + 5)
	// Forward each scalar param, then call the user function -> (data, len).
	for i := 0; i < nparams; i++ {
		body = inst.InstLocalGet(body, uint32(i))
	}
	body = inst.InstCall(body, userFuncIdx)
	body = inst.InstLocalSet(body, lenL) // len is on top
	body = inst.InstLocalSet(body, data) // then data
	// Normalize the SSO pair into a heap buffer (buf, byteLen).
	body = emitStrNormalize(body, idxs, data, lenL, buf, byteLen, iL)
	// ra = (__fern_alloc(12) + 3) & ~3 — the [ptr,len] return area must be
	// 4-byte aligned, but the bump allocator doesn't align.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstI32Const(body, 3)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, ra)
	// ra[0] = buf; ra[4] = byteLen.
	body = inst.InstLocalGet(body, ra)
	body = inst.InstLocalGet(body, buf)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, ra)
	body = inst.InstLocalGet(body, byteLen)
	body = memory.InstI32Store(body, 2, 4)
	// Return the return-area pointer.
	body = inst.InstLocalGet(body, ra)
	locals = inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return body, locals
}
