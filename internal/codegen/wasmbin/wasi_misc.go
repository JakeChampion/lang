// Miscellaneous runtime helpers wasmbin needed once the
// `buildComponent`-uses-wasmbin flip was attempted: slice
// header construction + indexing, the zero-copy
// `string.as_bytes()` view, and the stdio Writer
// constructors. Each one mirrors what the WAT path emits
// from internal/codegen/wasm/wasm.go.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// buildSliceMakeBody assembles __slice_make.
//
// Signature: (data, len: i32) → i32 — heap pointer to an
// 8-byte slice header `(data, len)` matching what the WAT
// path emits. data is stored at offset 0, len at offset 4.
// Used by the slice-syntax forms (`a[lo..hi]` etc.) and by
// `string.as_bytes()` to build a non-copying view.
//
// Locals (after the two params):
//
//	0: $data (param)
//	1: $len  (param)
//	2: $hdr  heap-allocated slice header
func buildSliceMakeBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 2)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 0) // data @ +0
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Store(body, 2, 0) // len @ +4
	body = inst.InstLocalGet(body, 2)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildSliceIdxBody builds the body for one of the strided
// __slice_idx_N helpers (N ∈ {1, 2, 4, 8}). Each one takes
// `(slice, i)` and returns the byte address of element `i`,
// trapping on negative or out-of-range indices. The IR picks
// the matching helper from the slice's element type.
//
// Locals (after the two params):
//
//	0: $slice (param) — slice header pointer
//	1: $i     (param) — element index
//	2: $data         — slice.data field
//	3: $len          — slice.len field
func buildSliceIdxBody(stride int32) func(map[string]uint32) []byte {
	return func(_ map[string]uint32) []byte {
		var body []byte
		// data = mem[$slice]
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		// len = mem[$slice + 4]
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 3)
		// i < 0 → trap
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32LtS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstUnreachable(body)
		body = inst.InstEnd(body)
		// i >= len (unsigned) → trap
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 3)
		body = numeric.InstI32GeU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstUnreachable(body)
		body = inst.InstEnd(body)
		// data + (i * stride)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 1)
		if stride != 1 {
			body = inst.InstI32Const(body, stride)
			body = numeric.InstI32Mul(body)
		}
		body = numeric.InstI32Add(body)
		locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildStringAsBytesBody assembles __method_string_as_bytes.
//
// Signature: (s_data, s_len: i32) → i32 — heap pointer to an
// 8-byte slice header `(data, len)` aliasing the source
// string's bytes. Inline-form strings (SSO-encoded with the
// high bit on `len`) get promoted to a fresh heap buffer
// first so the slice header's data field points at real
// linear memory — the caller's subsequent indexing /
// memcpy reads through it directly.
//
// Locals (after the two params):
//
//	0: $s_data    (param)
//	1: $s_len     (param)
//	2: $hdr       heap-allocated slice header
//	3: $byteLen   decoded byte length of the source string
//	4: $dataPtr   pointer to the bytes (input data for heap
//	              strings, freshly-allocated copy for inline)
//	5: $i         per-byte copy loop counter
func buildStringAsBytesBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	var body []byte
	// byteLen = __fern_str_len(s_data, s_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 3)
	// If inline (high bit of $s_len set), promote: alloc(byteLen)
	// + copy via __fern_str_byte. Otherwise reuse s_data directly.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		// Inline → heap promote.
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		// for i in 0..byteLen: mem[$dataPtr + i] = __fern_str_byte($s_data, $s_len, i)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 5)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 3)
			body = numeric.InstI32GeS(body)
			body = inst.InstBrIf(body, 1)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstCall(body, strByte)
			body = memory.InstI32Store8(body, 0, 0)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 5)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 4) // result of the if-block
	}
	body = inst.InstElse(body)
	{
		// Heap form: data ptr is the raw input.
		body = inst.InstLocalGet(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalSet(body, 4) // $dataPtr ← if-block result

	// hdr = alloc(8); mem[hdr] = dataPtr; mem[hdr+4] = byteLen.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 2)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 2)
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStdoutBody — () → i32. Allocates a 4-byte Writer struct
// `{ fd: i32 }` with fd=1 (stdout) and returns the pointer.
// Mirrors buildStdinBody (fd=0). The `__method_Writer_*`
// helpers dispatch on `w.fd`, so the same code covers stdio
// and file Writers.
func buildStdoutBody(idxs map[string]uint32) []byte {
	return buildFixedFdWriterBody(idxs, 1)
}

// buildStderrBody — () → i32. Same shape as buildStdoutBody
// with fd=2 (stderr).
func buildStderrBody(idxs map[string]uint32) []byte {
	return buildFixedFdWriterBody(idxs, 2)
}

func buildFixedFdWriterBody(idxs map[string]uint32, fd int32) []byte {
	alloc := idxs["__fern_alloc"]
	var body []byte
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0)
	body = inst.InstI32Const(body, fd)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
