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
	"github.com/jakechampion/lang/internal/ir"
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

// buildExternListResultWrapper is the integer-array counterpart of the
// string-result wrapper: it lowers a `list<T>` result the same way (canonical
// return area), then materializes a Fern `T[]` array. A native array is
// length-prefixed — the value is a pointer to the elements with the element
// count at `ptr-4` — so the wrapper allocates `4 + count*stride`, stores the
// element count, memory.copys the host bytes (`count*stride` of them) just past
// it, and returns the element pointer. `stride` is the element size in bytes (1
// for u8, 4 for i32, …); a stride of 1 emits the same bytes as the original
// u8-only wrapper. Wrapper type is (scalar params…) -> i32.
//
// Locals after the params: 0:$rb (return area) 1:$dp (host data) 2:$n (count)
// 3:$arr (array block base).
func buildExternListResultWrapper(nparams int, rawImport string, stride uint32) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		dp := uint32(nparams + 1)
		n := uint32(nparams + 2)
		arr := uint32(nparams + 3)

		// pushByteLen leaves `count*stride` (the byte length) on the stack. For
		// stride 1 it's just the count, so the u8 path is byte-for-byte the old
		// wrapper (no multiply emitted).
		pushByteLen := func(b []byte) []byte {
			b = inst.InstLocalGet(b, n)
			if stride != 1 {
				b = inst.InstI32Const(b, int32(stride))
				b = numeric.InstI32Mul(b)
			}
			return b
		}

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
		// dp = load(rb+0); n = load(rb+4) (element count).
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, dp)
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, n)
		// arr = __fern_alloc(4 + count*stride); store count; copy bytes to arr+4.
		body = inst.InstI32Const(body, 4)
		body = pushByteLen(body)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, arr)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstLocalGet(body, n)
		body = memory.InstI32Store(body, 2, 0) // count @ arr+0
		// memory.copy(arr+4, dp, count*stride)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, dp)
		body = pushByteLen(body)
		body = memory.InstMemoryCopy(body)
		// return arr + 4 (pointer to elements; count lives at ptr-4).
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternMemParamWrapper handles an extern with one or more memory
// parameters (`string` and/or `u8[]`) and a scalar (or void) result (P4c). Both
// kinds lower to the canonical `(ptr, len)` of contiguous bytes, but reach the
// wrapper differently:
//
//   - a `string` arrives as an SSO-encoded `(data, len)` pair (2 Fern slots)
//     whose data is not a raw pointer for inline strings, so it's normalized to
//     a heap buffer (emitStrNormalize) first;
//   - a `u8[]` arrives as a single element pointer (1 Fern slot) with the count
//     at `ptr-4`, and u8 is 1-byte stride, so its bytes are already a valid
//     canonical payload — forward `(ptr, load(ptr-4))` directly, no copy.
//
// Scalar params pass straight through. After forwarding all params the raw
// import is called; its scalar result (if any) is left on the stack. The
// wrapper's params mirror the Fern flattening (string→2, u8[]→1, scalar→1); 3
// i32 scratch locals (buf, byteLen, i), reused across string params, follow.
// buildExternRecordResultWrapper builds the wrapper for an `@import` extern
// returning a record (P4c). A multi-field record flattens to > 1 core value, so
// the canonical ABI returns it indirectly: the raw import takes a trailing
// return-area pointer and writes the record's fields there (canonical record
// layout). The wrapper allocs that return area, calls the import, then
// materializes a Fern struct — alloc `rcHeaderBytes + size`, set rc=1 at
// base+0, copy each field from the return area to `base + rcHeaderBytes +
// offset`, and return the user-visible pointer `base + rcHeaderBytes` — exactly
// how the struct constructor lays a struct out. Field offsets coincide between
// the canonical record and the Fern struct for 32-/64-bit scalar fields (same
// natural alignment). Wrapper type is (scalar params…) -> i32.
//
// Locals after the params: 0:$rb (return area) 1:$base (struct block).
func buildExternRecordResultWrapper(nparams int, rawImport string, rr *ir.ExternRecordResult) func(map[string]uint32) []byte {
	const rcHeaderBytes = 8
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		base := uint32(nparams + 1)

		var body []byte
		// rb = __fern_alloc(size) — 8-aligned canonical return area.
		body = inst.InstI32Const(body, rr.Size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, rb)
		// call import(params…, rb).
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// base = __fern_alloc(rcHeaderBytes + size); rc = 1 at base+0.
		body = inst.InstI32Const(body, rcHeaderBytes+rr.Size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, base)
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		// Copy each field: [base + rcHeaderBytes + off] = [rb + off].
		for _, f := range rr.Fields {
			off := uint32(f.Offset)
			vt := externRecordFieldValtype(f.Type)
			body = inst.InstLocalGet(body, base) // dst base (store offset adds rcHeaderBytes+off)
			body = inst.InstLocalGet(body, rb)   // src base (load offset adds off)
			switch vt {
			case encode.ValtypeI64:
				body = memory.InstI64Load(body, 3, off)
				body = memory.InstI64Store(body, 3, rcHeaderBytes+off)
			case encode.ValtypeF32:
				body = memory.InstF32Load(body, 2, off)
				body = memory.InstF32Store(body, 2, rcHeaderBytes+off)
			case encode.ValtypeF64:
				body = memory.InstF64Load(body, 3, off)
				body = memory.InstF64Store(body, 3, rcHeaderBytes+off)
			default:
				body = memory.InstI32Load(body, 2, off)
				body = memory.InstI32Store(body, 2, rcHeaderBytes+off)
			}
		}
		// return base + rcHeaderBytes (user-visible struct pointer).
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, rcHeaderBytes)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32) // rb, base
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternEnumResultWrapper builds the wrapper for an `@import` extern
// returning an option/result (P4c). The canonical variant flattens to
// (disc, payload) > 1 core value, so it returns indirectly: the raw import
// takes a trailing return-area pointer and writes `disc:u8 @0` + `payload @off`
// (the canonical variant memory layout — for these the payload offset matches
// the Fern box's). The wrapper reads them and materializes a Fern enum box
// like emitRepackPairAsHeapBox: alloc `rcHeaderBytes + size`, rc=1, the i32 tag
// at base+rcHeaderBytes (remapped 1-disc for option, since canonical
// none=0/some=1 is the reverse of Fern's Some=0/None=1), and the payload at
// base+rcHeaderBytes+off, returning base+rcHeaderBytes. Wrapper type is
// (scalar params…) -> i32.
//
// Locals after the params: 0:$rb (return area) 1:$base (box) 2:$disc.
func buildExternEnumResultWrapper(nparams int, rawImport string, ep *ir.ExternEnumParam) func(map[string]uint32) []byte {
	const rcHeaderBytes = 8
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		base := uint32(nparams + 1)
		disc := uint32(nparams + 2)
		poff := uint32(ep.PayloadOffset)
		payVT := externRecordFieldValtype(ep.PayloadType)
		psize := int32(4)
		if payVT == encode.ValtypeI64 || payVT == encode.ValtypeF64 {
			psize = 8
		}
		size := ep.PayloadOffset + psize // return-area + box field-area size

		var body []byte
		// rb = __fern_alloc(size) — 8-aligned canonical return area.
		body = inst.InstI32Const(body, size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, rb)
		// call import(params…, rb).
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// disc = load8_u @ rb+0.
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstLocalSet(body, disc)
		// base = __fern_alloc(rcHeaderBytes + size); rc = 1 at base+0.
		body = inst.InstI32Const(body, rcHeaderBytes+size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, base)
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		// tag (i32) @ base+rcHeaderBytes: remap 1-disc for option, else disc.
		body = inst.InstLocalGet(body, base)
		if ep.RemapDisc {
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalGet(body, disc)
			body = numeric.InstI32Sub(body)
		} else {
			body = inst.InstLocalGet(body, disc)
		}
		body = memory.InstI32Store(body, 2, rcHeaderBytes)
		// payload @ base+rcHeaderBytes+off = load(rb+off).
		body = inst.InstLocalGet(body, base)
		body = inst.InstLocalGet(body, rb)
		switch payVT {
		case encode.ValtypeI64:
			body = memory.InstI64Load(body, 3, poff)
			body = memory.InstI64Store(body, 3, uint32(rcHeaderBytes)+poff)
		case encode.ValtypeF32:
			body = memory.InstF32Load(body, 2, poff)
			body = memory.InstF32Store(body, 2, uint32(rcHeaderBytes)+poff)
		case encode.ValtypeF64:
			body = memory.InstF64Load(body, 3, poff)
			body = memory.InstF64Store(body, 3, uint32(rcHeaderBytes)+poff)
		default:
			body = memory.InstI32Load(body, 2, poff)
			body = memory.InstI32Store(body, 2, uint32(rcHeaderBytes)+poff)
		}
		// return base + rcHeaderBytes (user-visible enum pointer).
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, rcHeaderBytes)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32) // rb, base, disc
		return inst.PutFunctionBody(nil, locals, body)
	}
}

func buildExternMemParamWrapper(ex *ir.ExternFunc, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		imp := idxs[rawImport]
		nSlots := uint32(0)
		for _, p := range ex.Params {
			if isStringType(p.Type) {
				nSlots += 2
			} else {
				nSlots++ // u8[]/record/scalar are each one Fern slot
			}
		}
		bufL, byteLenL, iL := nSlots, nSlots+1, nSlots+2

		var body []byte
		slot := uint32(0)
		for i, p := range ex.Params {
			switch {
			case isStringType(p.Type):
				// (data@slot, len@slot+1) → normalized (bufL, byteLenL).
				body = emitStrNormalize(body, idxs, slot, slot+1, bufL, byteLenL, iL)
				body = inst.InstLocalGet(body, bufL)
				body = inst.InstLocalGet(body, byteLenL)
				slot += 2
			case ex.ParamRecords[i] != nil:
				// Record param: flatten to its fields. The Fern slot holds the
				// struct value; each field is loaded at its (rc-header-inclusive)
				// offset and pushed in declaration order, matching the canonical
				// record flattening the raw import expects.
				for _, f := range ex.ParamRecords[i] {
					body = inst.InstLocalGet(body, slot)
					off := uint32(f.Offset)
					switch externRecordFieldValtype(f.Type) {
					case encode.ValtypeI64:
						body = memory.InstI64Load(body, 3, off)
					case encode.ValtypeF32:
						body = memory.InstF32Load(body, 2, off)
					case encode.ValtypeF64:
						body = memory.InstF64Load(body, 3, off)
					default:
						body = memory.InstI32Load(body, 2, off)
					}
				}
				slot++
			case ex.ParamEnums[i] != nil:
				// option/result param: the Fern slot holds the enum box value.
				// Push the canonical discriminant (the i32 tag at +0, remapped
				// 1-tag for option), then the payload loaded at its box offset.
				ep := ex.ParamEnums[i]
				if ep.RemapDisc {
					body = inst.InstI32Const(body, 1)
					body = inst.InstLocalGet(body, slot)
					body = memory.InstI32Load(body, 2, 0) // tag @ +0
					body = numeric.InstI32Sub(body)       // 1 - tag
				} else {
					body = inst.InstLocalGet(body, slot)
					body = memory.InstI32Load(body, 2, 0)
				}
				poff := uint32(ep.PayloadOffset)
				body = inst.InstLocalGet(body, slot)
				switch externRecordFieldValtype(ep.PayloadType) {
				case encode.ValtypeI64:
					body = memory.InstI64Load(body, 3, poff)
				case encode.ValtypeF32:
					body = memory.InstF32Load(body, 2, poff)
				case encode.ValtypeF64:
					body = memory.InstF64Load(body, 3, poff)
				default:
					body = memory.InstI32Load(body, 2, poff)
				}
				slot++
			case isScalarArrayParamType(p.Type):
				// (ptr, len) = (elemPtr, load(elemPtr-4)). The count prefix holds
				// the element count, which is the canonical list length for any
				// element width; the elements are already packed at native stride.
				body = inst.InstLocalGet(body, slot) // ptr
				body = inst.InstLocalGet(body, slot)
				body = inst.InstI32Const(body, 4)
				body = numeric.InstI32Sub(body)
				body = memory.InstI32Load(body, 2, 0) // count @ ptr-4
				slot++
			default:
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
