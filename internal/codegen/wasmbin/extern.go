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
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// appendExternFieldLoad emits the load that reads a flattened record/tuple field
// of type t at byte offset off from a struct value, leaving its canonical core
// value on the stack. A Fern struct stores every sub-64-bit integer in a 4-byte
// (little-endian) slot, so a sub-word field (s8/s16/u8/u16) is read with a
// width+sign-aware load — i32.load8_s/u, i32.load16_s/u — to produce the
// correctly sign-/zero-extended i32 the canonical ABI flattens it to. Wider
// fields use the natural i64/f32/f64/i32 load matching externRecordFieldValtype.
func appendExternFieldLoad(body []byte, t ast.Type, off uint32) []byte {
	if _, ok := t.(ast.BoolType); ok {
		// bool is one byte (0/1) — read it zero-extended, both from the Fern
		// 4-byte slot (param) and the canonical 1-byte field (result).
		return memory.InstI32Load8U(body, 0, off)
	}
	if n, ok := t.(ast.NumberType); ok {
		switch n.NormalWidth() {
		case 8:
			if n.Signed {
				return memory.InstI32Load8S(body, 0, off)
			}
			return memory.InstI32Load8U(body, 0, off)
		case 16:
			if n.Signed {
				return memory.InstI32Load16S(body, 1, off)
			}
			return memory.InstI32Load16U(body, 1, off)
		}
	}
	switch externRecordFieldValtype(t) {
	case encode.ValtypeI64:
		return memory.InstI64Load(body, 3, off)
	case encode.ValtypeF32:
		return memory.InstF32Load(body, 2, off)
	case encode.ValtypeF64:
		return memory.InstF64Load(body, 3, off)
	}
	return memory.InstI32Load(body, 2, off)
}

// appendExternFieldStore stores a field's already-on-stack canonical value of
// type t into the Fern struct slot at byte offset off. The Fern slot is sized
// by the field's flat valtype — 4 bytes for any sub-64-bit integer (the value
// arrived sign-/zero-extended to i32), 8 for i64/f64, 4 for f32 — so a sub-word
// field is stored with a plain i32.store, not a narrowing store: the Fern struct
// widens it, matching how the constructor lays it out.
func appendExternFieldStore(body []byte, t ast.Type, off uint32) []byte {
	switch externRecordFieldValtype(t) {
	case encode.ValtypeI64:
		return memory.InstI64Store(body, 3, off)
	case encode.ValtypeF32:
		return memory.InstF32Store(body, 2, off)
	case encode.ValtypeF64:
		return memory.InstF64Store(body, 3, off)
	}
	return memory.InstI32Store(body, 2, off)
}

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

// emitAsyncAwaitLoop emits the WASI Preview-3 pending-await loop shared by every
// async-import wrapper. The `canon lower async` call's i32 status must be on the
// stack; this tees it into statusL and — when the state (status & 0xf) is not
// RETURNED (2), i.e. the import is pending — drives the waitable state machine
// over the subtask (status >> 4): `waitable-set.new` → `waitable.join` →
// `waitable-set.wait(set, rb+8)` → `subtask.drop` → `waitable-set.drop`. On
// return the result is in the lower's return area at rb+0, which the caller then
// reads. A synchronously-completing import takes the RETURNED fast path (no
// loop). The return area `rb` must be allocated >= 16 bytes (result at +0, the
// 8-byte wait event at +8). statusL/taskL/wsL are scratch i32 locals. The
// waitable intrinsics are imported (importSpecs `async_ws_new` etc.) and
// provided by the component composer's canon waitable-set.* / subtask.drop.
// See docs/WASI-PREVIEW3-ASYNC-PLAN.md.
func emitAsyncAwaitLoop(body []byte, idxs map[string]uint32, rbL, statusL, taskL, wsL uint32) []byte {
	wsn := idxs["async_ws_new"]
	wj := idxs["async_w_join"]
	wsw := idxs["async_ws_wait"]
	sd := idxs["async_subtask_drop"]
	wsd := idxs["async_ws_drop"]

	body = inst.InstLocalTee(body, statusL) // status (on stack) -> statusL, kept
	body = inst.InstI32Const(body, 0xf)
	body = numeric.InstI32And(body)
	body = inst.InstI32Const(body, 2) // RETURNED
	body = numeric.InstI32Ne(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, statusL) // subtask = status >> 4
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32ShrU(body)
	body = inst.InstLocalSet(body, taskL)
	body = inst.InstCall(body, wsn) // ws = waitable-set.new()
	body = inst.InstLocalSet(body, wsL)
	body = inst.InstLocalGet(body, taskL) // waitable.join(subtask, ws)
	body = inst.InstLocalGet(body, wsL)
	body = inst.InstCall(body, wj)
	body = inst.InstLocalGet(body, wsL) // waitable-set.wait(ws, rb+8); drop event
	body = inst.InstLocalGet(body, rbL)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, wsw)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, taskL) // subtask.drop(subtask)
	body = inst.InstCall(body, sd)
	body = inst.InstLocalGet(body, wsL) // waitable-set.drop(ws)
	body = inst.InstCall(body, wsd)
	body = inst.InstEnd(body)
	return body
}

// buildExternAsyncStringResultWrapper is the async counterpart of
// buildExternStringResultWrapper: the raw import is a `canon lower async` call
// `(scalar params…, retptr) -> i32 status`. After the call it runs the
// pending-await loop (emitAsyncAwaitLoop; a sync import takes the RETURNED fast
// path) and then lifts the canonical `(ptr, len)` at the return area into a Fern
// string. Wrapper type is (scalar params…) -> (i32 i32).
func buildExternAsyncStringResultWrapper(nparams int, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		lift := idxs["__bytes_to_lang_string"]
		imp := idxs[rawImport]
		retbuf := uint32(nparams)

		var body []byte
		// retbuf = (__fern_alloc(16) + 3) & ~3 — 4-byte-aligned (data @+0, len @+4,
		// the 8-byte wait event @+8).
		body = inst.InstI32Const(body, 16)
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
		// Await the (possibly pending) subtask, then lift the (ptr,len).
		body = emitAsyncAwaitLoop(body, idxs, retbuf, uint32(nparams)+1, uint32(nparams)+2, uint32(nparams)+3)
		// lift: __bytes_to_lang_string(load(retbuf+0), load(retbuf+4)).
		body = inst.InstLocalGet(body, retbuf)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, retbuf)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstCall(body, lift)

		locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32) // retbuf, status, task, ws
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

// buildExternAsyncListResultWrapper is the async counterpart of
// buildExternListResultWrapper: the raw import is a `canon lower async` call
// `(scalar params…, retptr) -> i32 status`, so after the call there is a status
// to DROP before reading the return-area (ptr,len) and materialising a Fern T[]
// (host bytes copied past a length prefix at native `stride`). For a
// synchronously-completing import the host has already written the canonical
// (ptr,len) into the return area (bytes materialised in this module's memory via
// the lower's realloc option). Wrapper type is (scalar params…) -> i32.
// Locals after the params: 0:$rb 1:$dp 2:$n 3:$arr.
func buildExternAsyncListResultWrapper(nparams int, rawImport string, stride uint32) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		dp := uint32(nparams + 1)
		n := uint32(nparams + 2)
		arr := uint32(nparams + 3)

		pushByteLen := func(b []byte) []byte {
			b = inst.InstLocalGet(b, n)
			if stride != 1 {
				b = inst.InstI32Const(b, int32(stride))
				b = numeric.InstI32Mul(b)
			}
			return b
		}

		var body []byte
		// rb = (__fern_alloc(16) + 3) & ~3 — 4-byte-aligned return area (ptr@+0,
		// len@+4, the 8-byte wait event @+8).
		body = inst.InstI32Const(body, 16)
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
		// Await the (possibly pending) subtask, then read the (ptr,len).
		body = emitAsyncAwaitLoop(body, idxs, rb, uint32(nparams)+4, uint32(nparams)+5, uint32(nparams)+6)
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
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, dp)
		body = pushByteLen(body)
		body = memory.InstMemoryCopy(body)
		// return arr + 4 (element pointer; count at ptr-4).
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32) // rb,dp,n,arr,status,task,ws
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternAsyncStreamResultWrapper handles an `@import async function f():
// stream[T]` (the checker rewrote the result to `T[]` and set StreamResultElem).
// The raw import is the `canon lower async` of the stream-returning import —
// `(scalar params…, retptr) -> i32 status`; the return area receives the stream
// *readable handle*. The wrapper awaits the lowered call (the dep task-returns
// the handle), then drives the colorless collect loop
// (docs/STREAM-TYPE-SURFACE.md): join the readable handle to a waitable-set and
// repeatedly `stream.read` directly into a grow-on-demand Fern array — on a
// BLOCKED read (`0xffffffff`) wait on the set and take the completed result from
// the event area (`evt+4 = (count<<4)|code`) — appending `count` elements each
// iteration until `code != 0` (CLOSED/EOF). Finally it writes the length prefix,
// `stream.drop-readable`s the handle, drops the set, and returns the Fern array
// element pointer. Wrapper type is `(scalar params…) -> i32`.
func buildExternAsyncStreamResultWrapper(nparams int, rawImport string, stride uint32) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		sread := idxs["async_stream_read"]
		sdropr := idxs["async_stream_drop_readable"]
		wsn := idxs["async_ws_new"]
		wj := idxs["async_w_join"]
		wsw := idxs["async_ws_wait"]
		wsd := idxs["async_ws_drop"]

		rb := uint32(nparams) // lower return area (handle@+0, lower-await event@+8)
		statusL := uint32(nparams + 1)
		taskL := uint32(nparams + 2)
		wsAwaitL := uint32(nparams + 3) // emitAsyncAwaitLoop scratch (the lower's subtask)
		rd := uint32(nparams + 4)       // stream readable handle
		ws := uint32(nparams + 5)       // read-loop waitable-set
		evt := uint32(nparams + 6)      // read-completion event buffer
		arr := uint32(nparams + 7)      // Fern array region: [len@+0][elems@+4]
		cap := uint32(nparams + 8)      // capacity in elements
		total := uint32(nparams + 9)    // elements collected so far
		rs := uint32(nparams + 10)      // read status / completed result
		avail := uint32(nparams + 11)   // free element slots = cap - total
		nd := uint32(nparams + 12)      // grown array region

		// elemBytes(countLocal) pushes countLocal*stride (bytes for `count` elems).
		elemBytes := func(b []byte, countLocal uint32) []byte {
			b = inst.InstLocalGet(b, countLocal)
			if stride != 1 {
				b = inst.InstI32Const(b, int32(stride))
				b = numeric.InstI32Mul(b)
			}
			return b
		}

		const initCap = 16
		var body []byte
		// rb = (alloc(16)+7) & ~7 — 8-byte-aligned lower return area.
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -8)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)
		// lower(scalar params…, rb) -> status; await it; rd = load(rb+0).
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		body = emitAsyncAwaitLoop(body, idxs, rb, statusL, taskL, wsAwaitL)
		body = inst.InstLocalGet(body, rb)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, rd)
		// evt = alloc(16); arr = alloc(4 + initCap*stride); cap = initCap; total = 0.
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, evt)
		body = inst.InstI32Const(body, int32(4+initCap*int(stride)))
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, arr)
		body = inst.InstI32Const(body, initCap)
		body = inst.InstLocalSet(body, cap)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, total)
		// ws = waitable-set.new(); waitable.join(rd, ws).
		body = inst.InstCall(body, wsn)
		body = inst.InstLocalSet(body, ws)
		body = inst.InstLocalGet(body, rd)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstCall(body, wj)

		body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // $done
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // $more
		// avail = cap - total; if avail == 0 grow (double the capacity, copy).
		body = inst.InstLocalGet(body, cap)
		body = inst.InstLocalGet(body, total)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalTee(body, avail)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, cap) // ncap = cap*2 (kept in cap after grow)
		body = inst.InstI32Const(body, 2)
		body = numeric.InstI32Mul(body)
		body = inst.InstLocalSet(body, cap)
		body = inst.InstI32Const(body, 4) // nd = alloc(4 + cap*stride)
		body = elemBytes(body, cap)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, nd)
		body = inst.InstLocalGet(body, nd) // memory.copy(nd+4, arr+4, total*stride)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = elemBytes(body, total)
		body = memory.InstMemoryCopy(body)
		body = inst.InstLocalGet(body, nd)
		body = inst.InstLocalSet(body, arr)
		body = inst.InstLocalGet(body, cap) // avail = cap - total
		body = inst.InstLocalGet(body, total)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, avail)
		body = inst.InstEnd(body) // if grow
		// rs = stream.read(rd, arr + 4 + total*stride, avail).
		body = inst.InstLocalGet(body, rd)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = elemBytes(body, total)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, avail)
		body = inst.InstCall(body, sread)
		body = inst.InstLocalSet(body, rs)
		// if rs == BLOCKED(-1): wait(ws, evt); rs = load(evt+4).
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, -1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstLocalGet(body, evt)
		body = inst.InstCall(body, wsw)
		body = inst.InstDrop(body)
		body = inst.InstLocalGet(body, evt)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, rs)
		body = inst.InstEnd(body)
		// total += rs >> 4 (the completed element count).
		body = inst.InstLocalGet(body, total)
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32ShrU(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, total)
		// if (rs & 0xf) != 0: break (CLOSED/EOF).
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, 0xf)
		body = numeric.InstI32And(body)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32Ne(body)
		body = inst.InstBrIf(body, 1) // break $done
		body = inst.InstBr(body, 0)   // continue $more
		body = inst.InstEnd(body)     // loop
		body = inst.InstEnd(body)     // block $done

		// store the length prefix; unjoin + drop the readable end + the set.
		body = inst.InstLocalGet(body, arr)
		body = inst.InstLocalGet(body, total)
		body = memory.InstI32Store(body, 2, 0) // count @ arr+0
		body = inst.InstLocalGet(body, rd)     // waitable.join(rd, 0) — unjoin before drops
		body = inst.InstI32Const(body, 0)
		body = inst.InstCall(body, wj)
		body = inst.InstLocalGet(body, rd)
		body = inst.InstCall(body, sdropr)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstCall(body, wsd)
		// return arr + 4 (element pointer; count at ptr-4).
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 13, encode.ValtypeI32) // rb,status,task,wsAwait,rd,ws,evt,arr,cap,total,rs,avail,nd
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternAsyncStreamParamWrapper handles an `@import async function
// sink(s: stream[T]): <scalar>` (the checker rewrote the param to `T[]` and set
// StreamParamElems). The single Fern argument is an eager `T[]`; the wrapper
// creates a stream, passes the readable end as the canonical param to the lowered
// call, write-streams the array's elements out over the wire (write-await), drops
// the writable end (EOF), then awaits the host's subtask and reads the scalar
// result (docs/STREAM-TYPE-SURFACE.md — the produce mirror of the collect-wrapper):
//
//	(rd, wr) = stream.new()                       // readable=lo32, writable=hi32
//	status = sink_lower(rd, retptr)               // host gets the read end
//	join(wr, ws); loop { rs = stream.write(wr, ptr + wrote*stride, count - wrote);
//	    if rs == 0xffffffff { wait(ws, evt); rs = load(evt+4) }
//	    wrote += rs>>4; if wrote>=count || (rs & 0xf) != 0 break }
//	unjoin(wr); stream.drop-writable(wr); ws.drop
//	await(status) via emitAsyncAwaitLoop; read scalar @ retptr
//
// Wrapper type is `(elemPtr) -> resultVT`. Scalar-only result; single stream param.
func buildExternAsyncStreamParamWrapper(rawImport string, resultVT byte, stride uint32) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		snew := idxs["async_stream_new"]
		swrite := idxs["async_stream_write"]
		sdropw := idxs["async_stream_drop_writable"]
		wsn := idxs["async_ws_new"]
		wj := idxs["async_w_join"]
		wsw := idxs["async_ws_wait"]
		wsd := idxs["async_ws_drop"]

		const ptr = 0          // the array's element pointer (the single Fern arg slot)
		h := uint32(1)         // stream.new packed handle
		rd := uint32(2)        // readable end
		wr := uint32(3)        // writable end
		rb := uint32(4)        // lower return area (result@+0, lower-await event@+8)
		statusL := uint32(5)   // captured lower status
		ws := uint32(6)        // write-loop waitable-set
		evt := uint32(7)       // write-completion event buffer
		count := uint32(8)     // element count (load(ptr-4))
		wrote := uint32(9)     // elements written so far
		rs := uint32(10)       // write status / completed result
		taskL := uint32(11)    // emitAsyncAwaitLoop scratch (subtask)
		wsAwaitL := uint32(12) // emitAsyncAwaitLoop scratch (its set)

		pushBytes := func(b []byte, n uint32) []byte { // n elems → n*stride bytes
			b = inst.InstLocalGet(b, n)
			if stride != 1 {
				b = inst.InstI32Const(b, int32(stride))
				b = numeric.InstI32Mul(b)
			}
			return b
		}

		var body []byte
		// h = stream.new(); rd = lo(h); wr = hi(h).
		body = inst.InstCall(body, snew)
		body = inst.InstLocalSet(body, h)
		body = inst.InstLocalGet(body, h)
		body = convert.InstI32WrapI64(body)
		body = inst.InstLocalSet(body, rd)
		body = inst.InstLocalGet(body, h)
		body = inst.InstI64Const(body, 32)
		body = numeric.InstI64ShrU(body)
		body = convert.InstI32WrapI64(body)
		body = inst.InstLocalSet(body, wr)
		// rb = (alloc(16)+7) & ~7; evt = alloc(16).
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -8)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, evt)
		// status = sink_lower(rd, rb) — pass the readable handle as the canonical param.
		body = inst.InstLocalGet(body, rd)
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		body = inst.InstLocalSet(body, statusL)
		// count = load(ptr-4); wrote = 0; ws = ws.new(); join(wr, ws).
		body = inst.InstLocalGet(body, ptr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, count)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, wrote)
		body = inst.InstCall(body, wsn)
		body = inst.InstLocalSet(body, ws)
		body = inst.InstLocalGet(body, wr)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstCall(body, wj)
		// write-stream loop.
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // $wdone
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)  // $wmore
		// rs = stream.write(wr, ptr + wrote*stride, count - wrote).
		body = inst.InstLocalGet(body, wr)
		body = inst.InstLocalGet(body, ptr)
		body = pushBytes(body, wrote)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, count)
		body = inst.InstLocalGet(body, wrote)
		body = numeric.InstI32Sub(body)
		body = inst.InstCall(body, swrite)
		body = inst.InstLocalSet(body, rs)
		// if rs == BLOCKED(-1): wait(ws, evt); rs = load(evt+4).
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, -1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstLocalGet(body, evt)
		body = inst.InstCall(body, wsw)
		body = inst.InstDrop(body)
		body = inst.InstLocalGet(body, evt)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, rs)
		body = inst.InstEnd(body)
		// wrote += rs>>4.
		body = inst.InstLocalGet(body, wrote)
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32ShrU(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, wrote)
		// if wrote >= count: break.
		body = inst.InstLocalGet(body, wrote)
		body = inst.InstLocalGet(body, count)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// if (rs & 0xf) != 0: break (CLOSED — reader went away).
		body = inst.InstLocalGet(body, rs)
		body = inst.InstI32Const(body, 0xf)
		body = numeric.InstI32And(body)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32Ne(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstBr(body, 0) // continue
		body = inst.InstEnd(body)   // loop
		body = inst.InstEnd(body)   // block $wdone
		// unjoin(wr); drop-writable(wr) [EOF]; ws.drop.
		body = inst.InstLocalGet(body, wr)
		body = inst.InstI32Const(body, 0)
		body = inst.InstCall(body, wj)
		body = inst.InstLocalGet(body, wr)
		body = inst.InstCall(body, sdropw)
		body = inst.InstLocalGet(body, ws)
		body = inst.InstCall(body, wsd)
		// Await the host subtask (the lower) and read the scalar result.
		body = inst.InstLocalGet(body, statusL)
		body = emitAsyncAwaitLoop(body, idxs, rb, statusL, taskL, wsAwaitL)
		body = inst.InstLocalGet(body, rb)
		switch resultVT {
		case encode.ValtypeI64:
			body = memory.InstI64Load(body, 3, 0)
		case encode.ValtypeF32:
			body = memory.InstF32Load(body, 2, 0)
		case encode.ValtypeF64:
			body = memory.InstF64Load(body, 3, 0)
		default:
			body = memory.InstI32Load(body, 2, 0)
		}

		return inst.PutFunctionBody(nil, streamParamLocals(), body)
	}
}

// streamParamLocals emits the produce-wrapper's locals as ONE 2-group vector:
// group 0 = a single i64 ($h, the packed stream.new handle, local index 1 after
// the param), group 1 = 11 i32 (rd,wr,rb,status,ws,evt,count,wrote,rs,task,
// wsAwait, indices 2..12). PutLocalsOneGroup can't be chained (it writes a whole
// 1-group vector each time), so this builds the vector directly.
func streamParamLocals() []byte {
	out := leb128.UlebU32(nil, 2)        // 2 groups
	out = leb128.UlebU32(out, 1)         // group 0: count 1
	out = append(out, encode.ValtypeI64) // i64 ($h)
	out = leb128.UlebU32(out, 11)        // group 1: count 11
	out = append(out, encode.ValtypeI32) // i32 scratch
	return out
}

// buildExternBoolListResultWrapper lifts a canonical `list<bool>` result into a
// Fern `bool[]`. Unlike the numeric-array wrapper (a straight memory.copy at
// native stride), a Fern bool array element is a 4-byte slot while the canonical
// `list<bool>` element is a single byte, so the host bytes must be byte-EXPANDED:
// the wrapper reads each canonical byte (i32.load8_u, a 0/1) and stores it as a
// 4-byte i32 element. The native array is length-prefixed (count at `ptr-4`), so
// it allocs `4 + count*4`, stores the count, runs the expand loop, and returns
// the element pointer. Wrapper type is (scalar params…) -> i32.
//
// Locals after the params: 0:$rb 1:$dp (host data) 2:$n (count) 3:$arr 4:$i.
func buildExternBoolListResultWrapper(nparams int, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		dp := uint32(nparams + 1)
		n := uint32(nparams + 2)
		arr := uint32(nparams + 3)
		i := uint32(nparams + 4)

		var body []byte
		// rb = (__fern_alloc(12) + 3) & ~3 — 4-byte aligned return area.
		body = inst.InstI32Const(body, 12)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 3)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -4)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)
		for p := 0; p < nparams; p++ {
			body = inst.InstLocalGet(body, uint32(p))
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
		// arr = __fern_alloc(4 + count*4); store count @ arr+0.
		body = inst.InstI32Const(body, 4)
		body = inst.InstLocalGet(body, n)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, arr)
		body = inst.InstLocalGet(body, arr)
		body = inst.InstLocalGet(body, n)
		body = memory.InstI32Store(body, 2, 0)
		// for i in 0..n: i32.store(arr+4+i*4, i32.load8_u(dp+i)).
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, i)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		body = inst.InstLocalGet(body, i)
		body = inst.InstLocalGet(body, n)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// store address: arr + 4 + i*4
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, i)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		// value: load8_u(dp + i)
		body = inst.InstLocalGet(body, dp)
		body = inst.InstLocalGet(body, i)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, i)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, i)
		body = inst.InstBr(body, 0)
		body = inst.InstEnd(body) // loop
		body = inst.InstEnd(body) // block
		// return arr + 4.
		body = inst.InstLocalGet(body, arr)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
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
// Locals after the params: 0:$rb (return area) 1:$base (struct block), then one
// scratch local per nesting depth for the inner structs.
func buildExternRecordResultWrapper(nparams int, rawImport string, rr *ir.ExternRecordResult) func(map[string]uint32) []byte {
	const rcHeaderBytes = 8
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		base := uint32(nparams + 1)
		// One scratch local per nesting level (innerLocals[d] holds the inner
		// struct base while materializing a field at depth d). Reused across
		// siblings at the same depth — each child's pointer is stored into its
		// parent before the next sibling overwrites the local.
		depth := rrNestDepth(rr)
		innerLocals := make([]uint32, depth)
		for d := 0; d < depth; d++ {
			innerLocals[d] = uint32(nparams + 2 + d)
		}

		var body []byte
		// rb = __fern_alloc(canonical size) — the canonical return-area memory
		// layout (sub-word fields pack tighter than the Fern struct's 4-byte slots).
		body = inst.InstI32Const(body, rr.CanonicalSize)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, rb)
		// call import(params…, rb).
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// base = __fern_alloc(rcHeaderBytes + Fern size); rc = 1 at base+0.
		body = inst.InstI32Const(body, rcHeaderBytes+rr.Size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, base)
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)

		// Materialize the struct's fields into `dst` (a local holding the block
		// base). A scalar leaf reads from the canonical area (width+sign-aware) and
		// stores into the Fern slot. A nested field allocs its own inner struct
		// (into innerLocals[d]), recurses to fill it, and stores its pointer.
		var emit func(body []byte, fields []ir.ExternRecordField, dst uint32, d int) []byte
		emit = func(body []byte, fields []ir.ExternRecordField, dst uint32, d int) []byte {
			for _, f := range fields {
				if f.Nested == nil {
					body = inst.InstLocalGet(body, dst) // store addr (Fern slot)
					body = inst.InstLocalGet(body, rb)  // load addr (canonical area)
					body = appendExternFieldLoad(body, f.Type, uint32(f.CanonicalOffset))
					body = appendExternFieldStore(body, f.Type, rcHeaderBytes+uint32(f.Offset))
					continue
				}
				child := innerLocals[d]
				// child = __fern_alloc(rcHeaderBytes + inner size); rc = 1.
				body = inst.InstI32Const(body, rcHeaderBytes+f.Nested.Size)
				body = inst.InstCall(body, alloc)
				body = inst.InstLocalSet(body, child)
				body = inst.InstLocalGet(body, child)
				body = inst.InstI32Const(body, 1)
				body = memory.InstI32Store(body, 2, 0)
				body = emit(body, f.Nested.Fields, child, d+1)
				// store (child + rcHeaderBytes) at dst + rcHeaderBytes + outer offset.
				body = inst.InstLocalGet(body, dst)
				body = inst.InstLocalGet(body, child)
				body = inst.InstI32Const(body, rcHeaderBytes)
				body = numeric.InstI32Add(body)
				body = memory.InstI32Store(body, 2, rcHeaderBytes+uint32(f.Offset))
			}
			return body
		}
		body = emit(body, rr.Fields, base, 0)

		// return base + rcHeaderBytes (user-visible struct pointer).
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, rcHeaderBytes)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, uint32(2+depth), encode.ValtypeI32) // rb, base, inner[0..depth)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// rrNestDepth returns the maximum record-nesting depth of an extern record
// result: 0 for an all-scalar (flat) record, 1 for one level of nested records,
// N for N levels — used to reserve one scratch local per level in the wrapper.
func rrNestDepth(rr *ir.ExternRecordResult) int {
	max := 0
	for _, f := range rr.Fields {
		if f.Nested != nil {
			if d := 1 + rrNestDepth(f.Nested); d > max {
				max = d
			}
		}
	}
	return max
}

// buildExternRecordResultDirectWrapper builds the wrapper for an `@import`
// extern returning a single-field record/tuple (P4c). A single-field composite
// flattens to exactly one core value, so the canonical ABI returns it by value
// (no return area): the raw import returns the field's core valtype directly.
// The wrapper calls it, then materializes the one-field Fern struct/tuple —
// alloc `rcHeaderBytes + size`, rc=1 at base+0, store the returned value at
// base+rcHeaderBytes (the single field is at offset 0), and return the
// user-visible pointer base+rcHeaderBytes. Wrapper type is (params…) -> i32.
//
// Locals after the params: 0:$base (struct block). The returned field value
// needs no local — the store address (base) is pushed first, then the import is
// called to leave the value on top, then the typed store consumes both.
func buildExternRecordResultDirectWrapper(nparams int, rawImport string, rr *ir.ExternRecordResult) func(map[string]uint32) []byte {
	const rcHeaderBytes = 8
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		vt := externRecordFieldValtype(rr.Fields[0].Type)
		base := uint32(nparams)

		var body []byte
		// base = __fern_alloc(rcHeaderBytes + size); rc = 1 at base+0.
		body = inst.InstI32Const(body, rcHeaderBytes+rr.Size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, base)
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		// Store the field at base + rcHeaderBytes (single field @ offset 0):
		// push the store address, then the import's by-value result, then store.
		body = inst.InstLocalGet(body, base)
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstCall(body, imp)
		switch vt {
		case encode.ValtypeI64:
			body = memory.InstI64Store(body, 3, rcHeaderBytes)
		case encode.ValtypeF32:
			body = memory.InstF32Store(body, 2, rcHeaderBytes)
		case encode.ValtypeF64:
			body = memory.InstF64Store(body, 3, rcHeaderBytes)
		default:
			body = memory.InstI32Store(body, 2, rcHeaderBytes)
		}
		// return base + rcHeaderBytes (user-visible struct pointer).
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, rcHeaderBytes)
		body = numeric.InstI32Add(body)

		locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32) // base
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildExternPlainEnumResultWrapper builds the wrapper for an `@import` extern
// returning a WIT `enum` (a Fern payloadless / C-style enum). The raw import
// returns a single i32 discriminant; the wrapper maps it to the matching static
// per-tag sentinel via the shared __enum_sent helper, producing a Fern enum
// value with no heap allocation. Wrapper type is (params…) -> i32.
func buildExternPlainEnumResultWrapper(nparams int, rawImport string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		imp := idxs[rawImport]
		sent := idxs["__enum_sent"]
		var body []byte
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstCall(body, imp)  // → disc on stack
		body = inst.InstCall(body, sent) // disc → sentinel ptr (the result)
		return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
	}
}

// buildEnumSentBody builds the `__enum_sent(disc) -> i32` helper: a select-chain
// mapping a runtime enum discriminant in 0..n-1 to the address of the static
// per-tag sentinel cell `[tag:i32 @0]` (interned via sentAddr). Sentinels are
// shared by tag value across all enums, so one helper covering 0..maxN-1 serves
// every WIT-enum result. No allocation — the returned pointer is immortal data.
func buildEnumSentBody(sentAddr func(int32) int, n int) func(map[string]uint32) []byte {
	return func(_ map[string]uint32) []byte {
		var body []byte
		if n <= 0 {
			n = 1
		}
		// acc = sentinel(n-1); for k = n-2..0:
		//   acc = select(acc, sent_k, disc != k)  ⇒  disc==k ? sent_k : acc.
		// select(a,b,c) returns a if c else b, popping a,b,c (a deepest), so the
		// running acc (already on the stack) is `a`/else, sent_k is `b`/then, and
		// the condition is `disc != k`.
		body = inst.InstI32Const(body, int32(sentAddr(int32(n-1))))
		for k := n - 2; k >= 0; k-- {
			body = inst.InstI32Const(body, int32(sentAddr(int32(k)))) // b (then)
			body = inst.InstLocalGet(body, 0)                         // disc
			body = inst.InstI32Const(body, int32(k))
			body = numeric.InstI32Ne(body) // c = (disc != k)
			body = inst.InstSelect(body)
		}
		return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
	}
}

// wasm opcodes the numeric helper package doesn't expose, emitted as raw bytes.
const (
	opI64ExtendI32U byte = 0xad // i64 <- i32 (zero-extend)
	opI32WrapI64    byte = 0xa7 // i32 <- i64 (low 32 bits)
)

// externEnumVariantIs64 reports whether a mixed-width variant arm's payload
// occupies the i64 half of the canonical join (a 64-bit int or f64). A 32-bit arm
// (incl. f32 / sub-word) occupies the i32 half and needs extend/wrap coercion.
func externEnumVariantIs64(t ast.Type) bool {
	vt := externRecordFieldValtype(t)
	return vt == encode.ValtypeI64 || vt == encode.ValtypeF64
}

// appendVariantParamPayloadI64 emits, for a mixed-width variant `@import`
// parameter, the i64 canonical join value: it branches on the box tag (at the box
// pointer in local `slot`) and, for the matching arm, loads the payload at that
// arm's box offset, coercing to i64 (64-bit arm: i64.load the bits; 32-bit arm:
// i32.load then zero-extend). A payloadless arm contributes i64.const 0 (the host
// drops the payload for that disc). Builds an n-1-deep if/else chain returning i64.
func appendVariantParamPayloadI64(body []byte, slot uint32, vs []ir.ExternEnumVariant) []byte {
	armLoad := func(b []byte, v ir.ExternEnumVariant) []byte {
		if v.Type == nil {
			return inst.InstI64Const(b, 0)
		}
		b = inst.InstLocalGet(b, slot)
		if externEnumVariantIs64(v.Type) {
			return memory.InstI64Load(b, 3, uint32(v.BoxOffset))
		}
		b = memory.InstI32Load(b, 2, uint32(v.BoxOffset))
		return append(b, opI64ExtendI32U)
	}
	n := len(vs)
	for k := 0; k < n-1; k++ {
		body = inst.InstLocalGet(body, slot)
		body = memory.InstI32Load(body, 2, 0) // tag @ box+0
		body = inst.InstI32Const(body, int32(k))
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, encode.ValtypeI64)
		body = armLoad(body, vs[k])
		body = inst.InstElse(body)
	}
	body = armLoad(body, vs[n-1]) // innermost (default) arm
	for k := 0; k < n-1; k++ {
		body = inst.InstEnd(body)
	}
	return body
}

// appendVariantParamMultiField emits, for a multi-field variant `@import`
// parameter, the SlotCount canonical join slots (ep.SlotTypes[j] per slot). For
// each slot j it builds an if/else chain on the box tag whose result type is the
// slot's valtype: the matching arm pushes its field j loaded from the field's box
// offset and coerced to the slot type, or the slot's zero if the arm has fewer
// than j+1 fields (padding). The coercion is byte-preserving: a field whose
// natural width matches the slot loads directly (an f32 field rides an i32 slot
// as its raw bits, an f64 an i64); a 32-bit field under an i64 slot zero-extends.
func appendVariantParamMultiField(body []byte, slot uint32, ep *ir.ExternEnumParam) []byte {
	n := len(ep.Variants)
	slotVal := func(b []byte, v ir.ExternEnumVariant, j int32, slotVT byte) []byte {
		if int(j) >= len(v.Fields) {
			return appendZeroConst(b, slotVT) // padding: shorter arm
		}
		off := uint32(v.Fields[j])
		b = inst.InstLocalGet(b, slot)
		switch slotVT {
		case encode.ValtypeF32:
			return memory.InstF32Load(b, 2, off)
		case encode.ValtypeF64:
			return memory.InstF64Load(b, 3, off)
		case encode.ValtypeI64:
			if externFieldIs64(v.FieldTypes[j]) {
				return memory.InstI64Load(b, 3, off)
			}
			b = memory.InstI32Load(b, 2, off)
			return append(b, opI64ExtendI32U) // 32-bit arm → i64 slot
		default:
			return memory.InstI32Load(b, 2, off)
		}
	}
	for j := int32(0); j < ep.SlotCount; j++ {
		slotVT := externRecordFieldValtype(ep.SlotTypes[j])
		for k := 0; k < n-1; k++ {
			body = inst.InstLocalGet(body, slot)
			body = memory.InstI32Load(body, 2, 0) // tag @ box+0
			body = inst.InstI32Const(body, int32(k))
			body = numeric.InstI32Eq(body)
			body = inst.InstIfStart(body, slotVT)
			body = slotVal(body, ep.Variants[k], j, slotVT)
			body = inst.InstElse(body)
		}
		body = slotVal(body, ep.Variants[n-1], j, slotVT) // innermost (default) arm
		for k := 0; k < n-1; k++ {
			body = inst.InstEnd(body)
		}
	}
	return body
}

// externFieldIs64 reports whether a multi-field arm's field occupies 8 bytes (a
// 64-bit int or f64), so it loads/stores as i64; otherwise it is a 4-byte field.
func externFieldIs64(t ast.Type) bool {
	vt := externRecordFieldValtype(t)
	return vt == encode.ValtypeI64 || vt == encode.ValtypeF64
}

// appendZeroConst pushes the zero value of a core valtype.
func appendZeroConst(b []byte, vt byte) []byte {
	switch vt {
	case encode.ValtypeI64:
		return inst.InstI64Const(b, 0)
	case encode.ValtypeF32:
		return inst.InstF32Const(b, 0)
	case encode.ValtypeF64:
		return inst.InstF64Const(b, 0)
	default:
		return inst.InstI32Const(b, 0)
	}
}

// appendVariantResultStoreMultiField emits, for a multi-field variant `@import`
// *result*, the per-arm payload store: branching on the disc, the matching arm
// copies each of its fields from the canonical return-area (the variant memory
// layout — field j @ rb + FieldAreaOffsets[j]) into the Fern box at the field's
// box offset. The copy is by field width (i64 for an 8-byte field, i32 for a
// 4-byte one) and byte-preserving, so a float field's bits round-trip through the
// integer move.
func appendVariantResultStoreMultiField(body []byte, base, rb, disc uint32, ep *ir.ExternEnumParam) []byte {
	const rcHeaderBytes = 8
	storeArm := func(b []byte, v ir.ExternEnumVariant) []byte {
		for j, off := range v.Fields {
			area := uint32(v.FieldAreaOffsets[j])
			box := uint32(rcHeaderBytes) + uint32(off)
			b = inst.InstLocalGet(b, base) // store address
			b = inst.InstLocalGet(b, rb)
			if externFieldIs64(v.FieldTypes[j]) {
				b = memory.InstI64Load(b, 3, area)
				b = memory.InstI64Store(b, 3, box)
			} else {
				b = memory.InstI32Load(b, 2, area)
				b = memory.InstI32Store(b, 2, box)
			}
		}
		return b
	}
	n := len(ep.Variants)
	for k := 0; k < n-1; k++ {
		body = inst.InstLocalGet(body, disc)
		body = inst.InstI32Const(body, int32(k))
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = storeArm(body, ep.Variants[k])
		body = inst.InstElse(body)
	}
	body = storeArm(body, ep.Variants[n-1]) // innermost (default) arm
	for k := 0; k < n-1; k++ {
		body = inst.InstEnd(body)
	}
	return body
}

// appendVariantResultStore emits, for a mixed-width variant `@import` *result*,
// the per-arm payload store: it branches on the disc (local `disc`) and, for the
// matching arm, reads the i64 canonical join slot at `rb+poff` and stores it into
// the Fern box (base local) at that arm's box offset — i64.store for a 64-bit arm,
// i32.wrap_i64 + i32.store for a 32-bit arm. A payloadless arm stores nothing.
func appendVariantResultStore(body []byte, base, rb, disc, poff uint32, vs []ir.ExternEnumVariant) []byte {
	const rcHeaderBytes = 8
	storeArm := func(b []byte, v ir.ExternEnumVariant) []byte {
		if v.Type == nil {
			return b // payloadless — nothing to store
		}
		b = inst.InstLocalGet(b, base) // store address
		b = inst.InstLocalGet(b, rb)
		b = memory.InstI64Load(b, 3, poff) // i64 join slot
		if externEnumVariantIs64(v.Type) {
			return memory.InstI64Store(b, 3, uint32(rcHeaderBytes)+uint32(v.BoxOffset))
		}
		b = append(b, opI32WrapI64)
		return memory.InstI32Store(b, 2, uint32(rcHeaderBytes)+uint32(v.BoxOffset))
	}
	n := len(vs)
	for k := 0; k < n-1; k++ {
		body = inst.InstLocalGet(body, disc)
		body = inst.InstI32Const(body, int32(k))
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = storeArm(body, vs[k])
		body = inst.InstElse(body)
	}
	body = storeArm(body, vs[n-1]) // innermost (default) arm
	for k := 0; k < n-1; k++ {
		body = inst.InstEnd(body)
	}
	return body
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
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)
		base := uint32(nparams + 1)
		disc := uint32(nparams + 2)
		areaSize, _ := enumResultAreaBoxSize(ep)

		var body []byte
		// rb = __fern_alloc(areaSize) — 8-aligned canonical return area.
		body = inst.InstI32Const(body, areaSize)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, rb)
		// call import(params…, rb).
		for i := 0; i < nparams; i++ {
			body = inst.InstLocalGet(body, uint32(i))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// Read the filled return area into a fresh Fern enum box.
		body = appendEnumResultAreaToBox(body, idxs, rb, base, disc, ep)

		locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32) // rb, base, disc
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// enumResultAreaBoxSize returns the canonical return-area size (where the import
// writes disc + payload) and the Fern enum-box payload size for an option/result
// `@import` result layout. Shared by the plain and mem-param result wrappers.
func enumResultAreaBoxSize(ep *ir.ExternEnumParam) (areaSize, boxSize int32) {
	payVT := externRecordFieldValtype(ep.PayloadType)
	psize := int32(4)
	if payVT == encode.ValtypeI64 || payVT == encode.ValtypeF64 {
		psize = 8
	}
	areaSize = ep.PayloadOffset + psize // canonical return area (disc + join slot)
	boxSize = areaSize
	if ep.SlotCount > 0 {
		// Multi-field: the canonical return-area is the variant memory layout
		// (ir precomputed AreaSize); the Fern box holds the widest arm's fields
		// at their box offsets (8 bytes for a 64-bit field, else 4).
		areaSize = ep.AreaSize
		boxSize = 0
		for _, v := range ep.Variants {
			for j, off := range v.Fields {
				w := int32(4)
				if externFieldIs64(v.FieldTypes[j]) {
					w = 8
				}
				if end := off + w; end > boxSize {
					boxSize = end
				}
			}
		}
	} else if ep.Variants != nil {
		boxSize = 0
		for _, v := range ep.Variants {
			if v.Type == nil {
				continue
			}
			w := int32(4)
			if externEnumVariantIs64(v.Type) {
				w = 8
			}
			if end := v.BoxOffset + w; end > boxSize {
				boxSize = end
			}
		}
	}
	// The Fern box (boxSize) holds only the modelled payload, but the canonical
	// return area must accommodate whatever the host writes for the *real* WIT
	// result — which can be a wider variant than the modelled arms (e.g.
	// `result<_, error-code>`, whose `error-code` is a 39-case variant). A
	// handler that reads such a result discriminant-only models it as a
	// same-width-scalar `Result` (a small box) but the host still writes the full
	// variant into the area. Floor the area at 64 bytes — the canonical-ABI retptr
	// bound the embedded HTTP path relies on (wasi_http.go: "Each canonical-ABI
	// retptr fits in 64 bytes") — so the import can never overrun the area. The
	// box is unaffected; only the scratch area grows.
	const canonRetptrFloor = 64
	if areaSize < canonRetptrFloor {
		areaSize = canonRetptrFloor
	}
	return areaSize, boxSize
}

// appendEnumResultAreaToBox emits the code that, given a filled canonical return
// area `rb` (disc:u8 @0, payload @off), materializes a fresh Fern enum box
// `[rc][tag@0][payload@off]` and leaves its user-visible pointer on the stack.
// `base` and `disc` are scratch i32 locals. Shared by the plain and mem-param
// result wrappers.
func appendEnumResultAreaToBox(body []byte, idxs map[string]uint32, rb, base, disc uint32, ep *ir.ExternEnumParam) []byte {
	const rcHeaderBytes = 8
	alloc := idxs["__fern_alloc"]
	poff := uint32(ep.PayloadOffset)
	payVT := externRecordFieldValtype(ep.PayloadType)
	_, boxSize := enumResultAreaBoxSize(ep)
	// disc = load8_u @ rb+0.
	body = inst.InstLocalGet(body, rb)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstLocalSet(body, disc)
	// base = __fern_alloc(rcHeaderBytes + boxSize); rc = 1 at base+0.
	body = inst.InstI32Const(body, rcHeaderBytes+boxSize)
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
	if ep.SlotCount > 0 {
		// Multi-field: store each of the matched arm's fields from its i32 join
		// slot into the box, branching on the disc.
		body = appendVariantResultStoreMultiField(body, base, rb, disc, ep)
	} else if ep.Variants != nil {
		// Mixed-width: store the matched arm's payload (coerced from the i64
		// join slot) at that arm's box offset, branching on the disc.
		body = appendVariantResultStore(body, base, rb, disc, poff, ep.Variants)
	} else {
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
	}
	// leave base + rcHeaderBytes (user-visible enum pointer) on the stack.
	body = inst.InstLocalGet(body, base)
	body = inst.InstI32Const(body, rcHeaderBytes)
	body = numeric.InstI32Add(body)
	return body
}

// externParamSlots returns the number of Fern stack slots an extern's params
// occupy (a `string` is the two-word (data, len) pair; every other param —
// scalar, numeric/bool array, record, tuple, enum — is one slot). It is also the
// base index of the (bufL, byteLenL, iL) marshalling scratch locals.
func externParamSlots(ex *ir.ExternFunc) uint32 {
	n := uint32(0)
	for _, p := range ex.Params {
		if isStringType(p.Type) {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// emitExternParamMarshal pushes the canonical argument(s) for every @import
// parameter onto the stack, in declaration order — the shared marshalling head
// used by BOTH the sync (buildExternMemParamWrapper) and async
// (buildExternAsyncMemParamWrapper) extern wrappers, so an async @import accepts
// exactly the parameter shapes a sync @import does. A `string` normalises through
// the SSO seam into (bufL, byteLenL); a record/tuple flattens to its fields; an
// option/result pushes (disc, payload-join); a plain enum pushes its tag; a
// numeric array forwards (elemPtr, count@ptr-4); a bool array byte-repacks into a
// fresh (buf, count); a scalar passes through. bufL/byteLenL/iL are i32 scratch
// locals at (externParamSlots(ex), +1, +2).
func emitExternParamMarshal(body []byte, idxs map[string]uint32, ex *ir.ExternFunc, bufL, byteLenL, iL uint32) []byte {
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
				// Nested record leaf: deref each offset in the path (load the
				// inner value pointer) before the final leaf load.
				for _, off := range f.DerefPath {
					body = memory.InstI32Load(body, 2, uint32(off))
				}
				body = appendExternFieldLoad(body, f.Type, uint32(f.Offset))
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
			if ep.SlotCount > 0 {
				// Multi-field variant: push SlotCount i32 join slots, each chosen
				// by branching on the box tag (arm's field j, or 0 to pad).
				body = appendVariantParamMultiField(body, slot, ep)
			} else if ep.Variants != nil {
				// Mixed-width variant: produce the i64 join value by branching on
				// the box tag — each arm loads its payload at its own box offset
				// and coerces to i64 (a 32-bit arm extends; a 64-bit arm loads it
				// directly; float bits are value-preserving under the int load).
				body = appendVariantParamPayloadI64(body, slot, ep.Variants)
			} else {
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
			}
			slot++
		case ex.ParamPlainEnums[i]:
			// plain (payloadless) enum → WIT enum: the Fern slot holds a
			// pointer to a 4-byte sentinel/box `[tag:i32 @0]`; push the tag as
			// the canonical discriminant (Fern variant order == WIT case order,
			// no remap).
			body = inst.InstLocalGet(body, slot)
			body = memory.InstI32Load(body, 2, 0)
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
		case isBoolArrayParamType(p.Type):
			// bool[] → canonical list<bool> (1 byte/elem): the Fern bools are
			// 4-byte slots, so byte-repack into a fresh count-byte buffer and
			// push (buf, count). byteLenL holds the count (== byte length for
			// 1-byte elements). The buffer pointer is pushed onto the stack
			// now, so a later param reusing bufL doesn't disturb it.
			body = inst.InstLocalGet(body, slot) // count = load(ptr-4)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Sub(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, byteLenL)
			body = inst.InstLocalGet(body, byteLenL) // buf = alloc(count)
			body = inst.InstCall(body, idxs["__fern_alloc"])
			body = inst.InstLocalSet(body, bufL)
			body = inst.InstI32Const(body, 0) // i = 0
			body = inst.InstLocalSet(body, iL)
			body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
			body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
			body = inst.InstLocalGet(body, iL) // if i >= count: break
			body = inst.InstLocalGet(body, byteLenL)
			body = numeric.InstI32GeU(body)
			body = inst.InstBrIf(body, 1)
			body = inst.InstLocalGet(body, bufL) // store8(buf+i, load(ptr+i*4))
			body = inst.InstLocalGet(body, iL)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, slot)
			body = inst.InstLocalGet(body, iL)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = memory.InstI32Store8(body, 0, 0)
			body = inst.InstLocalGet(body, iL) // i++
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, iL)
			body = inst.InstBr(body, 0)
			body = inst.InstEnd(body)            // loop
			body = inst.InstEnd(body)            // block
			body = inst.InstLocalGet(body, bufL) // push (buf, count)
			body = inst.InstLocalGet(body, byteLenL)
			slot++
		default:
			body = inst.InstLocalGet(body, slot)
			slot++
		}
	}
	return body
}

func buildExternMemParamWrapper(ex *ir.ExternFunc, rawImport string, resultEnum *ir.ExternEnumParam) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		imp := idxs[rawImport]
		nSlots := externParamSlots(ex)
		bufL, byteLenL, iL := nSlots, nSlots+1, nSlots+2

		var body []byte
		if resultEnum != nil {
			// Composite (option/result) result alongside the memory param(s): alloc
			// the canonical return area up front; the import gets it as a trailing
			// retptr and returns void.
			areaSize, _ := enumResultAreaBoxSize(resultEnum)
			body = inst.InstI32Const(body, areaSize)
			body = inst.InstCall(body, idxs["__fern_alloc"])
			body = inst.InstLocalSet(body, nSlots+3) // rb
		}
		body = emitExternParamMarshal(body, idxs, ex, bufL, byteLenL, iL)
		if resultEnum != nil {
			// Pass the return-area pointer as the trailing canonical retptr, call
			// (void), then read the filled area into a Fern enum box.
			rb, base, disc := nSlots+3, nSlots+4, nSlots+5
			body = inst.InstLocalGet(body, rb)
			body = inst.InstCall(body, imp)
			body = appendEnumResultAreaToBox(body, idxs, rb, base, disc, resultEnum)
			// buf, byteLen, i, rb, base, disc
			locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
			return inst.PutFunctionBody(nil, locals, body)
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

// buildExportListResultWrapper builds the core wrapper for a numeric-array
// (`list<T>`) returning `@export` function (P6 — docs/WIT-BRING-YOUR-OWN.md).
// The Fern function compiles to a core func returning a single i32 — the
// element pointer of a length-prefixed array (count at `ptr-4`, elements packed
// at native stride). The canonical ABI for a `func(...) -> list<T>` export
// returns a single i32 pointer to a `[data_ptr, len]` return area (the memory
// lift reads it). Because a Fern numeric array is already contiguous at the
// canonical element stride, the wrapper needs no copy: it forwards the scalar
// params, calls the user func, reads the count from `ptr-4`, and writes the
// 4-byte-aligned `[ptr, count]` return area (the simpler sibling of the
// string-result wrapper, which must SSO-normalize first).
//
// Locals after the params: 0:$arr (element ptr) 1:$ra (return area) 2:$count.
func buildExportListResultWrapper(idxs map[string]uint32, userFuncIdx uint32, nparams int) (body []byte, locals []byte) {
	arr := uint32(nparams)
	ra := uint32(nparams + 1)
	count := uint32(nparams + 2)
	// Forward each scalar param, then call the user function -> arr (element ptr).
	for i := 0; i < nparams; i++ {
		body = inst.InstLocalGet(body, uint32(i))
	}
	body = inst.InstCall(body, userFuncIdx)
	body = inst.InstLocalSet(body, arr)
	// count = i32.load(arr - 4) — the element count in the array's length prefix.
	body = inst.InstLocalGet(body, arr)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, count)
	// ra = (__fern_alloc(8) + 3) & ~3 — the [ptr,len] return area must be 4-byte
	// aligned (it holds two i32s), but the bump allocator doesn't align.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstI32Const(body, 3)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, ra)
	// ra[0] = arr (the element pointer, already contiguous); ra[4] = count.
	body = inst.InstLocalGet(body, ra)
	body = inst.InstLocalGet(body, arr)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, ra)
	body = inst.InstLocalGet(body, count)
	body = memory.InstI32Store(body, 2, 4)
	// Return the return-area pointer.
	body = inst.InstLocalGet(body, ra)
	locals = inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return body, locals
}

// buildExportSumTypeResultWrapper builds the core wrapper for an `@export`
// function returning an Option[T] / Result[T,E] — a WIT `option` / `result`
// (P6 — docs/WIT-BRING-YOUR-OWN.md). The Fern function returns an enum box value
// pointer V (`[tag:i32@0][payload@off]`), but the canonical sum flattens to
// (disc, payload) > 1 core value, so it returns indirectly through a return area
// (`disc:u8@0`, `payload@off`) the memory lift reads. The wrapper forwards the
// scalar params, calls the user func, and writes the area: the discriminant
// remapped `1-tag` for option (canonical none=0/some=1 reverses Fern's
// Some=0/None=1; result's Ok=0/Err=1 matches), the payload copied at the same
// box offset. Wrapper type: (scalar params…) -> i32.
//
// `pairForm` selects how the user func's result arrives: a pair-form function
// returns (tag:i32, payload:i32) directly on the stack; otherwise it returns a
// single enum box value pointer.
//
// Locals after the params: pair-form 0:$tag 1:$pay 2:$rb; box-form 0:$v 1:$rb.
func buildExportSumTypeResultWrapper(idxs map[string]uint32, userFuncIdx uint32, nparams int, ep *ir.ExternEnumParam, pairForm bool) (body []byte, locals []byte) {
	poff := uint32(ep.PayloadOffset)
	payVT := externRecordFieldValtype(ep.PayloadType)
	psize := int32(4)
	if payVT == encode.ValtypeI64 || payVT == encode.ValtypeF64 {
		psize = 8
	}
	areaSize := int32(ep.PayloadOffset) + psize

	allocRB := func(b []byte, rb uint32) []byte {
		// rb = (__fern_alloc(areaSize+7) + 7) & ~7 — 8-byte aligned return area
		// (the canonical variant load aligns to the widest field; the bump
		// allocator doesn't align).
		b = inst.InstI32Const(b, areaSize+7)
		b = inst.InstCall(b, idxs["__fern_alloc"])
		b = inst.InstI32Const(b, 7)
		b = numeric.InstI32Add(b)
		b = inst.InstI32Const(b, -8)
		b = numeric.InstI32And(b)
		return inst.InstLocalSet(b, rb)
	}
	// Forward each scalar param.
	for i := 0; i < nparams; i++ {
		body = inst.InstLocalGet(body, uint32(i))
	}
	body = inst.InstCall(body, userFuncIdx)

	if pairForm {
		tag := uint32(nparams)
		pay := uint32(nparams + 1)
		rb := uint32(nparams + 2)
		body = inst.InstLocalSet(body, pay) // payload is on top
		body = inst.InstLocalSet(body, tag)
		body = allocRB(body, rb)
		// disc:u8 @ rb+0 — the tag, remapped 1-tag for option.
		body = inst.InstLocalGet(body, rb)
		if ep.RemapDisc {
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalGet(body, tag)
			body = numeric.InstI32Sub(body)
		} else {
			body = inst.InstLocalGet(body, tag)
		}
		body = memory.InstI32Store8(body, 0, 0)
		// payload @ rb+off — the pair-form payload is a single i32.
		body = inst.InstLocalGet(body, rb)
		body = inst.InstLocalGet(body, pay)
		body = memory.InstI32Store(body, 2, poff)
		body = inst.InstLocalGet(body, rb)
		locals = inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
		return body, locals
	}

	v := uint32(nparams)
	rb := uint32(nparams + 1)
	body = inst.InstLocalSet(body, v)
	body = allocRB(body, rb)
	// disc:u8 @ rb+0 — the box tag, remapped 1-tag for option.
	body = inst.InstLocalGet(body, rb)
	if ep.RemapDisc {
		body = inst.InstI32Const(body, 1)
		body = inst.InstLocalGet(body, v)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32Sub(body)
	} else {
		body = inst.InstLocalGet(body, v)
		body = memory.InstI32Load(body, 2, 0)
	}
	body = memory.InstI32Store8(body, 0, 0)
	// payload @ rb+off — copied from the box at the same offset.
	body = inst.InstLocalGet(body, rb)
	body = inst.InstLocalGet(body, v)
	body = appendExternFieldLoad(body, ep.PayloadType, poff)
	body = appendExternFieldStore(body, ep.PayloadType, poff)
	body = inst.InstLocalGet(body, rb)
	locals = inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return body, locals
}

// buildExportTupleResultWrapper builds the core wrapper for an `@export`
// function returning a tuple `(A, B, …)` — a WIT `tuple` (P6 —
// docs/WIT-BRING-YOUR-OWN.md). The Fern function returns a tuple value pointer V
// (elements packed at `V+field.Offset`), but a multi-element tuple flattens to
// > 1 core value, so the canonical ABI returns it indirectly through a return
// area (elements at `field.CanonicalOffset`). The wrapper forwards the scalar
// params, calls the user func, and copies each element from the tuple value to
// the area. Wrapper type: (scalar params…) -> i32.
//
// Locals after the params: 0:$v (tuple value pointer) 1:$rb (return area).
func buildExportTupleResultWrapper(idxs map[string]uint32, userFuncIdx uint32, nparams int, rr *ir.ExternRecordResult) (body []byte, locals []byte) {
	v := uint32(nparams)
	rb := uint32(nparams + 1)
	for i := 0; i < nparams; i++ {
		body = inst.InstLocalGet(body, uint32(i))
	}
	body = inst.InstCall(body, userFuncIdx)
	body = inst.InstLocalSet(body, v)
	// rb = (__fern_alloc(CanonicalSize+7) + 7) & ~7 — 8-byte aligned return area.
	body = inst.InstI32Const(body, rr.CanonicalSize+7)
	body = inst.InstCall(body, idxs["__fern_alloc"])
	body = inst.InstI32Const(body, 7)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -8)
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, rb)
	// Copy each element: area[CanonicalOffset] = tuple[Offset].
	for i := range rr.Fields {
		f := rr.Fields[i]
		body = inst.InstLocalGet(body, rb)
		body = inst.InstLocalGet(body, v)
		body = appendExternFieldLoad(body, f.Type, uint32(f.Offset))
		body = appendExternFieldStore(body, f.Type, uint32(f.CanonicalOffset))
	}
	body = inst.InstLocalGet(body, rb)
	locals = inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return body, locals
}

// funcHasNumericArrayParam reports whether any of fn's parameters is a numeric
// array (`list<T>`) — the shape that needs the export PARAM wrapper (the others,
// scalars + strings, map onto the core signature directly).
func funcHasNumericArrayParam(fn *ir.Func) bool {
	for _, p := range fn.Params {
		if isScalarArrayParamType(p.Type) {
			return true
		}
	}
	return false
}

// buildExportListParamWrapper builds the core wrapper for an `@export` function
// taking one or more numeric-array (`list<T>`) parameters (P6 —
// docs/WIT-BRING-YOUR-OWN.md). The canonical ABI materialises each incoming list
// in the core's memory (via cabi_realloc) and passes (ptr, len); a Fern array is
// instead a single pointer to length-prefixed elements (count at ptr-4), so the
// wrapper builds that array — alloc `4 + len*stride`, store the count, then
// memory.copy the elements — and calls the user function with the element
// pointer. A string parameter forwards its (ptr,len) directly (it already
// matches the two-word string), scalars pass through, and the function's
// scalar/void result is returned as-is. Wrapper type: (canonical-flattened
// params…) -> result.
//
// Locals after the params: one i32 per array param, holding its Fern array base.
func buildExportListParamWrapper(idxs map[string]uint32, userFuncIdx uint32, params []ast.Param) (body []byte, locals []byte) {
	const kScalar, kString, kArray = 0, 1, 2
	type pslot struct {
		kind   int
		start  uint32 // first wrapper-param slot
		stride uint32 // element stride (array only)
	}
	var slots []pslot
	var cur uint32
	narr := 0
	for _, p := range params {
		switch {
		case isScalarArrayParamType(p.Type):
			slots = append(slots, pslot{kArray, cur, scalarArrayElemStride(p.Type)})
			cur += 2
			narr++
		case isStringType(p.Type):
			slots = append(slots, pslot{kString, cur, 0})
			cur += 2
		default:
			slots = append(slots, pslot{kScalar, cur, 0})
			cur++
		}
	}
	baseLocal := cur // first array-base scratch local
	alloc := idxs["__fern_alloc"]

	pushByteLen := func(b []byte, lenL uint32, stride uint32) []byte {
		b = inst.InstLocalGet(b, lenL)
		if stride != 1 {
			b = inst.InstI32Const(b, int32(stride))
			b = numeric.InstI32Mul(b)
		}
		return b
	}

	// Materialize each array param into a length-prefixed Fern array.
	ai := uint32(0)
	for _, s := range slots {
		if s.kind != kArray {
			continue
		}
		base := baseLocal + ai
		lenL := s.start + 1
		// base = __fern_alloc(4 + len*stride)
		body = inst.InstI32Const(body, 4)
		body = pushByteLen(body, lenL, s.stride)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, base)
		// count @ base+0
		body = inst.InstLocalGet(body, base)
		body = inst.InstLocalGet(body, lenL)
		body = memory.InstI32Store(body, 2, 0)
		// memory.copy(base+4, ptr, len*stride)
		body = inst.InstLocalGet(body, base)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, s.start)
		body = pushByteLen(body, lenL, s.stride)
		body = memory.InstMemoryCopy(body)
		ai++
	}
	// Push call args in declaration order, then call.
	ai = 0
	for _, s := range slots {
		switch s.kind {
		case kArray:
			body = inst.InstLocalGet(body, baseLocal+ai)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body) // element pointer (count at ptr-4)
			ai++
		case kString:
			body = inst.InstLocalGet(body, s.start)
			body = inst.InstLocalGet(body, s.start+1)
		default:
			body = inst.InstLocalGet(body, s.start)
		}
	}
	body = inst.InstCall(body, userFuncIdx)
	locals = inst.PutLocalsOneGroup(nil, uint32(narr), encode.ValtypeI32)
	return body, locals
}

// buildExternAsyncScalarResultWrapper returns the body builder for the wrapper
// of an `@import ... async function` with scalar params and a scalar result —
// the WASI Preview-3 colorless async-import shape (docs/WASI-PREVIEW3-ASYNC-PLAN.md).
// The raw import is lowered with `canon lower async`, whose core signature is
// `(scalar params…, retptr) -> i32 status`: the result is delivered indirectly
// through a linear-memory return area, and the i32 status reports
// completion/blocked. For an import that completes synchronously the result is
// already in the return area when the call returns, so the wrapper drops the
// status and loads the result directly — no waitable-set loop. (A pending
// import would additionally need `waitable-set.wait`; that lands with the
// non-blocking-host path.) Keeping the wrapper between the Fern `dep()` call and
// the raw async-lowered import is what makes the await colorless: the source
// just sees a value-returning call.
//
// nparams is the count of (scalar, one-slot) parameters; rawImport is the raw
// import's name in helperIdxs; resultVT is the result's core valtype. The
// wrapper's type is (scalar params…) -> resultVT.
//
// Local after the params: 0:$rb (8-byte-aligned return area).
func buildExternAsyncScalarResultWrapper(nparams int, rawImport string, resultVT byte) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		rb := uint32(nparams)

		var body []byte
		// rb = (__fern_alloc(16) + 7) & ~7 — an 8-byte-aligned return area
		// (covers an i64/f64 result; __fern_alloc bumps without aligning, so
		// over-allocate by 7 bytes of slack and round up).
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -8)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)
		// raw import: forward each scalar param, then the return-area pointer.
		for p := 0; p < nparams; p++ {
			body = inst.InstLocalGet(body, uint32(p))
		}
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// Await the (possibly pending) subtask, then read the result inline.
		body = emitAsyncAwaitLoop(body, idxs, rb, uint32(nparams)+1, uint32(nparams)+2, uint32(nparams)+3)
		body = inst.InstLocalGet(body, rb)
		switch resultVT {
		case encode.ValtypeI64:
			body = memory.InstI64Load(body, 3, 0)
		case encode.ValtypeF32:
			body = memory.InstF32Load(body, 2, 0)
		case encode.ValtypeF64:
			body = memory.InstF64Load(body, 3, 0)
		default:
			body = memory.InstI32Load(body, 2, 0)
		}

		locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32) // rb, status, task, ws
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// asyncResultKind selects how an async-import wrapper lifts the value the host
// wrote into the return area after the awaited call: a scalar read, a `string`
// lift, or a numeric `list<T>` copy.
type asyncResultKind int

const (
	asyncResScalar asyncResultKind = iota // i32/i64/f32/f64 read at rb+0
	asyncResString                        // (ptr,len)@rb lifted via __bytes_to_lang_string
	asyncResList                          // (ptr,len)@rb copied into a length-prefixed Fern array
)

// buildExternAsyncMemParamWrapper handles an `@import ... async function` whose
// params are any mix of scalars, `string`s, and numeric arrays (`u8[]`/`i32[]`/
// `f64[]`/…), returning a scalar, a `string`, or a numeric `list<T>` — the
// realistic multi-arg edge-handler shape (e.g. `fetch(url: string, timeout: i32)
// -> string`, `post(id: i32, body: u8[]) -> i32`). It generalises (and subsumes)
// the single-string / single-array param wrappers.
//
// Each argument is marshalled to its canonical slot(s) in declaration order:
//   - scalar → forwarded as-is (one i32/i64/f32/f64);
//   - string → SSO-normalised (emitStrNormalize) into a contiguous heap buffer,
//     forwarded as the canonical (ptr, len) the callee reads via the lower's
//     memory option;
//   - numeric array → forwarded as (elemPtr, count@elemPtr-4); a Fern array is
//     already canonical (elements packed at native stride), so no copy.
//
// Then the return-area pointer is appended (the trailing canonical retptr) and
// the async-lower call `(canon params…, retptr) -> i32 status` runs; the
// pending-await loop drives a non-RETURNED subtask. The result tail is selected
// by resKind: a scalar is read at rb+0; a `string`/`list` value (whose bytes the
// host materialised in this module's memory via the lower's realloc option — so
// the module must export cabi_realloc and the composer set NeedsRealloc) is
// lifted from the canonical (ptr, len) at rb+0/rb+4. The param bytes themselves
// are the caller's (memory option, no realloc needed for them).
//
// Locals after the Fern param slots: $buf $byteLen $i (string-normalise scratch),
// $rb (return area), $status $task $ws (await-loop scratch), and for a list
// result $dp $n $arr.
func buildExternAsyncMemParamWrapper(ex *ir.ExternFunc, rawImport string, resKind asyncResultKind, resultVT byte, stride uint32) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		imp := idxs[rawImport]
		// Fern-facing slots: string = 2 (data, len); every other param = 1.
		nSlots := externParamSlots(ex)
		bufL, byteLenL, iL := nSlots, nSlots+1, nSlots+2
		rb, statusL, taskL, wsL := nSlots+3, nSlots+4, nSlots+5, nSlots+6
		dp, nL, arr := nSlots+7, nSlots+8, nSlots+9 // list-result scratch

		var body []byte
		// rb = (__fern_alloc(16) + 7) & ~7 — 8-byte-aligned return area (result @ +0,
		// the 8-byte wait event @ +8). Allocated first so later string-buffer allocs
		// don't disturb it.
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -8)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, rb)

		// Marshal each param to its canonical slot(s) — the shared sync/async head,
		// so an async @import accepts every parameter shape a sync one does
		// (scalar / string / numeric+bool array / record / tuple / option / result).
		body = emitExternParamMarshal(body, idxs, ex, bufL, byteLenL, iL)
		// Trailing retptr, then the async-lower call -> status.
		body = inst.InstLocalGet(body, rb)
		body = inst.InstCall(body, imp)
		// Await the (possibly pending) subtask, then lift the result by kind.
		body = emitAsyncAwaitLoop(body, idxs, rb, statusL, taskL, wsL)

		switch resKind {
		case asyncResString:
			// __bytes_to_lang_string(load(rb+0), load(rb+4)).
			body = inst.InstLocalGet(body, rb)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalGet(body, rb)
			body = memory.InstI32Load(body, 2, 4)
			body = inst.InstCall(body, idxs["__bytes_to_lang_string"])
		case asyncResList:
			// dp = load(rb+0); n = load(rb+4) (element count); copy count*stride
			// bytes into a fresh length-prefixed array, return its element pointer.
			pushByteLen := func(b []byte) []byte {
				b = inst.InstLocalGet(b, nL)
				if stride != 1 {
					b = inst.InstI32Const(b, int32(stride))
					b = numeric.InstI32Mul(b)
				}
				return b
			}
			body = inst.InstLocalGet(body, rb)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, dp)
			body = inst.InstLocalGet(body, rb)
			body = memory.InstI32Load(body, 2, 4)
			body = inst.InstLocalSet(body, nL)
			body = inst.InstI32Const(body, 4)
			body = pushByteLen(body)
			body = numeric.InstI32Add(body)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalSet(body, arr)
			body = inst.InstLocalGet(body, arr)
			body = inst.InstLocalGet(body, nL)
			body = memory.InstI32Store(body, 2, 0) // count @ arr+0
			body = inst.InstLocalGet(body, arr)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, dp)
			body = pushByteLen(body)
			body = memory.InstMemoryCopy(body)
			body = inst.InstLocalGet(body, arr)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body) // return arr + 4 (count at ptr-4)
		default: // asyncResScalar
			body = inst.InstLocalGet(body, rb)
			switch resultVT {
			case encode.ValtypeI64:
				body = memory.InstI64Load(body, 3, 0)
			case encode.ValtypeF32:
				body = memory.InstF32Load(body, 2, 0)
			case encode.ValtypeF64:
				body = memory.InstF64Load(body, 3, 0)
			default:
				body = memory.InstI32Load(body, 2, 0)
			}
		}

		nScratch := uint32(7)
		if resKind == asyncResList {
			nScratch = 10 // + dp, n, arr
		}
		locals := inst.PutLocalsOneGroup(nil, nScratch, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}
