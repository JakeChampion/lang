package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// A handle's `drop_cache(offset, len)` is posix_fadvise's DONTNEED, which
// both previews carry as advice on a descriptor: preview 1's fd_advise and
// preview 2's descriptor.advise. The host may ignore advice, and then the
// call answers None as it would on Linux.

// wasiAdviceDontneed is `dontneed` in both previews' advice enum.
const wasiAdviceDontneed = 4

// buildFdDropCacheBodyP1 builds `__fern_fd_drop_cache` on preview 1:
// fd_advise(fd, offset, len, dontneed), None on errno 0 and Some(IoError)
// otherwise.
//
// Signature: (r: i32, offset: i64, len: i64) -> i32 (heap-form
// Option[IoError]).
//
// Locals after the params: 3: $errno  4: $err_ptr  5: $box
func buildFdDropCacheBodyP1(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	advise := idxs["wasi_fd_advise"]

	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, wasiAdviceDontneed)
	body = inst.InstCall(body, advise)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 3, 4, 5)
	}
	body = inst.InstEnd(body)

	body = emitPayloadlessResultBox(body, allocRc1, 5, 8, 1) // None
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildFdDropCacheBodyP2 is the preview-2 `__fern_fd_drop_cache`:
// descriptor.advise on the descriptor the handle was opened on. A stdio
// handle owns no descriptor and answers Unsupported, as `stat` does.
//
// Locals after the params: 3: $rb  4: $desc  5: $errno  6: $err_ptr  7: $box
func buildFdDropCacheBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	advise := idxs["wasi_descriptor_advise_p2"]

	body := emitClosedSomeP2(nil, idxs, 0, 5, 6, 7)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalTee(body, 4)
	body = inst.InstI32Const(body, noDescriptor)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, errnoNoTsup)
		body = inst.InstLocalSet(body, 5)
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 5, 6, 7)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, wasiAdviceDontneed)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, advise)

	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 3, 5, emptyOkErrorCodeOff)
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 5, 6, 7)
	}
	body = inst.InstEnd(body)

	body = emitPayloadlessResultBox(body, allocRc1, 7, 8, 1) // None
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
