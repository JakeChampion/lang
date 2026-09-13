package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// Write-back of a handle: `fsync` and `fdatasync` are preview 1's
// fd_sync / fd_datasync and preview 2's descriptor.sync / sync-data,
// while `syncfs` exists in neither and answers Unsupported on both.
//
// A preopen is a capability handle rather than a mount, so there is no
// set of filesystems for a component to flush and no descriptor whose
// filesystem could be named. ENOTSUP reaches the caller through the
// errno table `__build_io_error` already carries, so the refusal is the
// same `IoError::Unsupported` a host reports when it declines a call.

// emitSomeIoError wraps the IoError built from `errnoLocal` in
// `Some`, at Option[IoError]'s uniform 8-byte box, and returns it.
// `errLocal` and `boxLocal` are scratch.
func emitSomeIoError(body []byte, buildIoErr, allocRc1 uint32, errnoLocal, errLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstI32Const(body, 0) // path_data = 0 (empty)
	body = inst.InstI32Const(body, 0) // path_len  = 0
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, errLocal)

	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag 0 = Some
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, errLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	return inst.InstReturn(body)
}

// buildFdSyncBodyP1 builds `__fern_fd_fsync` / `__fern_fd_fdatasync` on
// preview 1 from the import that backs it: one call against the
// handle's fd, None on errno 0 and Some(IoError) otherwise. The shape
// is buildCloseBody's, which is the same contract with a different
// import.
//
// Signature: (r: i32) -> i32 (heap-form Option[IoError]).
//
// Locals after the param: 1: $errno  2: $err_ptr  3: $box
func buildFdSyncBodyP1(importName string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		allocRc1 := idxs["__fern_alloc_rc1"]
		buildIoErr := idxs["__build_io_error"]
		call := idxs[importName]

		var body []byte
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstCall(body, call)
		body = inst.InstLocalTee(body, 1)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = emitSomeIoError(body, buildIoErr, allocRc1, 1, 2, 3)
		}
		body = inst.InstEnd(body)

		body = emitPayloadlessResultBox(body, allocRc1, 3, 8, 1) // None
		locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildFdSyncBodyP2 builds the same pair on preview 2 from the
// descriptor method that backs it. A stdio handle owns no descriptor and
// answers Unsupported, as `stat` does for the same handle.
//
// Locals after the param: 1: $desc  2: $rb  3: $errno  4: $err_ptr  5: $box
func buildFdSyncBodyP2(methodIdx string) func(map[string]uint32) []byte {
	return func(idxs map[string]uint32) []byte {
		alloc := idxs["__fern_alloc"]
		allocRc1 := idxs["__fern_alloc_rc1"]
		buildIoErr := idxs["__build_io_error"]
		call := idxs[methodIdx]

		var body []byte
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalTee(body, 1)
		body = inst.InstI32Const(body, noDescriptor)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, errnoNoTsup)
			body = inst.InstLocalSet(body, 3)
			body = emitSomeIoError(body, buildIoErr, allocRc1, 3, 4, 5)
		}
		body = inst.InstEnd(body)

		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, call)

		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = appendErrnoFromErrorCodeAt(body, idxs, 2, 3, emptyOkErrorCodeOff)
			body = emitSomeIoError(body, buildIoErr, allocRc1, 3, 4, 5)
		}
		body = inst.InstEnd(body)

		body = emitPayloadlessResultBox(body, allocRc1, 5, 8, 1) // None
		locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
}

// buildFdSyncfsBody is `__fern_fd_syncfs` on both previews: neither has
// a per-filesystem flush, so it is ENOTSUP unconditionally, which
// `__build_io_error` turns into `IoError::Unsupported`.
//
// Locals after the param: 1: $errno  2: $err_ptr  3: $box
func buildFdSyncfsBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]

	var body []byte
	body = inst.InstI32Const(body, errnoNoTsup)
	body = inst.InstLocalSet(body, 1)
	body = emitSomeIoError(body, buildIoErr, allocRc1, 1, 2, 3)

	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
