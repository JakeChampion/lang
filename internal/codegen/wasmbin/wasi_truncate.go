// `truncate` for the wasmbin backend.
//
// The builtin is path-based (see the checker's doc comment: no Fern open
// mode yields a writable descriptor to an existing file without O_TRUNC
// having already emptied it, so an fd form could not express an extend).
// WASI has no path-based set-size on either preview — measured against
// wasmtime 46, `wasi_snapshot_preview1::path_filestat_set_size` is not a
// defined import, and `wasi:filesystem/types@0.2.0` has `set-size` only
// as a descriptor method. So both bodies here are open, set the size,
// drop: the length is written through a descriptor the helper owns for
// the duration of the call and never hands out.
//
// The open carries neither CREATE nor TRUNCATE, which is what keeps the
// helper faithful to `truncate(2)`: a missing path is the host's
// no-such-file error rather than a fresh empty file, and an existing
// one is not emptied before the size is set.
//
// Both paths resolve under the first preopen, like the rest of the `fs`
// family — an escaping path is ENOTCAPABLE where a kernel would resolve
// it.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
)

// buildTruncateBody assembles __fern_truncate.
//
// Signature: (path_data, path_len, length: i64) → i32 — heap-form
// Result[void, IoError].
//
// path_open under the fd-3 preopen, fd_filestat_set_size, fd_close. The
// close runs before the size errno is inspected so the descriptor is
// released on both arms.
//
// Locals after the three params:
//
//	3: $rb  4: $path_buf  5: $path_byte_len  6: $i (normalize scratch)
//	7: $fd  8: $errno  9: $err_ptr  10: $box
func buildTruncateBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	pathOpen := idxs["wasi_path_open"]
	setSize := idxs["wasi_fd_filestat_set_size"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte
	// The return area comes first: path_open writes the new fd as a
	// u32 there and the host enforces 4-byte alignment, which the
	// string normalisations below would otherwise disturb.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 6)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 1) // dirflags: follow a final symlink
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 0) // oflags: no CREATE, no TRUNCATE
	body = inst.InstI64Const(body, wasiRightFdWrite|wasiRightFdFilestatSetSize)
	body = inst.InstI64Const(body, wasiRightFdWrite|wasiRightFdFilestatSetSize)
	body = inst.InstI32Const(body, 0) // fdflags
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 8)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 8, 0, 1, 9, 10)
	}
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 7)

	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, setSize)
	body = inst.InstLocalSet(body, 8)

	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, fdClose)
	body = inst.InstDrop(body)

	body = inst.InstLocalGet(body, 8)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 8, 0, 1, 9, 10)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 9)
	body = emitResultOkPtr(body, allocRc1, 9, 10)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTruncateBodyP2 is the preview-2 buildTruncateBody:
// get-directories → open-at → descriptor.set-size → resource-drop.
//
// Locals after the three params:
//
//	3: $rb  4: $path_buf  5: $path_byte_len  6: $i (normalize scratch)
//	7: $preopen  8: $desc  9: $errno  10: $err_ptr  11: $box
func buildTruncateBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	setSize := idxs["wasi_descriptor_set_size_p2"]
	descDrop := idxs["wasi_descriptor_drop_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 6)
	body = emitPreopenP2(body, alloc, getDirs, 3, 7)

	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 1) // path-flags: symlink-follow
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 0) // open-flags: no CREATE, no TRUNCATE
	body = inst.InstI32Const(body, wasiP2DescFlagWrite)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, openAt)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, idxs, 3, 9)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 9, 0, 1, 10, 11)
	}
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 8)

	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, setSize)
	// The discriminant is read before the drop, which reuses nothing
	// in the return area but does invalidate the descriptor.
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstLocalSet(body, 9)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstCall(body, descDrop)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 3, 9, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 9, 0, 1, 10, 11)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10)
	body = emitResultOkPtr(body, allocRc1, 10, 11)

	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
