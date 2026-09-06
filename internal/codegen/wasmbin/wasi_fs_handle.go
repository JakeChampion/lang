// The two Reader / Writer methods that ask the HANDLE a question rather
// than moving bytes through it: `stat()` (fstat) and `seek(offset,
// whence)` (lseek). Both are the shape that separates the previews most
// sharply — preview 1 has an fd and asks it directly, preview 2 has a
// stream plus the descriptor it was opened on, and only the descriptor
// can answer.
//
// The preview-2 Reader is therefore {stream @0, descriptor @4, pos @8},
// the position being what read-via-stream cannot be asked back: every
// read advances it, and seek replaces the stream with one opened at the
// new offset. A stdio handle has no descriptor (noDescriptor) and answers
// `stat` with Unsupported and `seek` with ESPIPE — the same pair a pipe
// answers on a kernel.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/leb128"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// errnoSpipe is preview-1 ESPIPE, what a handle with no descriptor
// answers a seek with — the same answer a pipe gives lseek(2).
const errnoSpipe int32 = 70

// readerPosOff is the byte offset, inside the preview-2 Reader's data
// area, of the i64 stream position.
const readerPosOff uint32 = 8

// putLocalsI32I64 encodes a two-group locals vector: n32 i32 slots then
// n64 i64 slots, numbered in that order after the params.
func putLocalsI32I64(n32, n64 uint32) []byte {
	buf := leb128.UlebU32(nil, 2)
	buf = leb128.UlebU32(buf, n32)
	buf = append(buf, encode.ValtypeI32)
	buf = leb128.UlebU32(buf, n64)
	return append(buf, encode.ValtypeI64)
}

// emitHandleResultErr appends "classify `errnoLocal` against an empty
// path, wrap it in Err, return the box" — the error path every handle
// method shares, since a read, a write, an fstat or an lseek carries no
// path to name.
func emitHandleResultErr(body []byte, buildIoErr, allocBox, errnoLocal, errPtrLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, errPtrLocal)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 1) // tag = Err
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, errPtrLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	return inst.InstReturn(body)
}

// emitHandleResultOkI64 appends "wrap the i64 in `valLocal` in Ok and
// leave the box on the stack": a 16-byte box, tag at 0, the payload at 8
// — where `payloadLayout` puts an 8-byte payload behind a 4-byte tag.
func emitHandleResultOkI64(body []byte, allocBox, valLocal, boxLocal uint32) []byte {
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag = Ok
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstLocalGet(body, valLocal)
	body = memory.InstI64Store(body, 3, 8)
	return inst.InstLocalGet(body, boxLocal)
}

// buildFdStatBody assembles __fern_fd_stat on preview 1.
//
// Signature: (r) → i32 — heap-form Result[FileStat, IoError].
// fd_filestat_get(mem[r], buf) into the same 64-byte record
// path_filestat_get writes, projected by projectFilestatP1.
//
// Locals after the param:
//
//	5: $errno  6: $stat_buf  7: $filetype  8: $fs  9: $box
func buildFdStatBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	filestatGet := idxs["wasi_fd_filestat_get"]

	var body []byte
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6)

	// errno = fd_filestat_get(mem[r], buf)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, filestatGet)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleResultErr(body, buildIoErr, allocBox, 5, 7, 9)
	}
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 6)
	body = memory.InstI32Load8U(body, 0, filestatFiletypeOff)
	body = inst.InstLocalSet(body, 7)
	body = projectFilestatP1(body, alloc, 6, 7, 8)
	body = emitResultOkPtr(body, allocBox, 8, 9)

	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildFdStatBodyP2 is the preview-2 __fern_fd_stat: descriptor.stat on
// the descriptor the handle was opened on, projected by
// projectDescriptorStatP2. A stdio handle owns no descriptor and answers
// Unsupported.
//
// Locals after the param:
//
//	2: $rb  6: $errno  7: $type  8: $fs  9: $box  10: $desc
func buildFdStatBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	descStat := idxs["wasi_descriptor_stat_p2"]

	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalTee(body, 10)
	body = inst.InstI32Const(body, noDescriptor)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, errnoNoTsup)
		body = inst.InstLocalSet(body, 6)
		body = emitHandleResultErr(body, buildIoErr, allocBox, 6, 7, 9)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, statAtRetBytes)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, descStat)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 2, 6, statAtTypeOff)
		body = emitHandleResultErr(body, buildIoErr, allocBox, 6, 7, 9)
	}
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, statAtTypeOff)
	body = inst.InstLocalSet(body, 7)
	body = projectDescriptorStatP2(body, alloc, 2, 7, 8)
	body = emitResultOkPtr(body, allocBox, 8, 9)

	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderSeekBody assembles __fern_reader_seek on preview 1.
//
// Signature: (r, offset: i64, whence) → i32 — heap-form
// Result[i64, IoError]. One fd_seek; the new offset comes back through
// an 8-byte return area.
//
// Locals after the three params:
//
//	3: $errno  4: $rb  5: $errptr  6: $box
func buildReaderSeekBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	fdSeek := idxs["wasi_fd_seek"]

	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)

	// errno = fd_seek(mem[r], offset, whence, rb)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, fdSeek)
	body = inst.InstLocalSet(body, 3)

	body = inst.InstLocalGet(body, 3)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleResultErr(body, buildIoErr, allocBox, 3, 5, 6)
	}
	body = inst.InstEnd(body)

	// Ok(mem64[rb]) — reuse the offset param slot for the value.
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI64Load(body, 3, 0)
	body = inst.InstLocalSet(body, 1)
	body = emitHandleResultOkI64(body, allocBox, 1, 6)

	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderSeekBodyP2 is the preview-2 __fern_reader_seek. A stream
// has no position to move, so the seek is: resolve the target from
// whence (SEEK_CUR from the Reader's own count, SEEK_END from
// descriptor.stat's size), open a new read-via-stream at it, drop the
// old stream, record the position. A handle with no descriptor answers
// ESPIPE, a negative target EINVAL — the two refusals lseek(2) gives.
//
// Locals after the three params:
//
//	i32 — 3: $errno  4: $rb  5: $errptr  6: $box  7: $desc  8: $stream
//	i64 — 9: $target
func buildReaderSeekBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	descStat := idxs["wasi_descriptor_stat_p2"]
	readVia := idxs["wasi_descriptor_read_via_stream_p2"]
	streamDrop := idxs["wasi_io_input_stream_drop"]

	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalTee(body, 7)
	body = inst.InstI32Const(body, noDescriptor)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, errnoSpipe)
		body = inst.InstLocalSet(body, 3)
		body = emitHandleResultErr(body, buildIoErr, allocBox, 3, 5, 6)
	}
	body = inst.InstEnd(body)

	// target = offset, then adjusted by whence.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalSet(body, 9)
	// SEEK_CUR: target += pos
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI64Load(body, 3, readerPosOff)
		body = numeric.InstI64Add(body)
		body = inst.InstLocalSet(body, 9)
	}
	body = inst.InstEnd(body)
	// SEEK_END: target += size, from descriptor.stat.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 2)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, statAtRetBytes)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, descStat)
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = appendErrnoFromErrorCodeAt(body, idxs, 4, 3, statAtTypeOff)
			body = emitHandleResultErr(body, buildIoErr, allocBox, 3, 5, 6)
		}
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI64Load(body, 3, statAtSizeOff)
		body = numeric.InstI64Add(body)
		body = inst.InstLocalSet(body, 9)
	}
	body = inst.InstEnd(body)
	// target < 0 → EINVAL.
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI64Const(body, 0)
	body = numeric.InstI64LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, errnoInval)
		body = inst.InstLocalSet(body, 3)
		body = emitHandleResultErr(body, buildIoErr, allocBox, 3, 5, 6)
	}
	body = inst.InstEnd(body)

	// read-via-stream(desc, target, rb) → the new stream, opened before
	// the old one is dropped so a refusal leaves the Reader as it was.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, readVia)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, idxs, 4, 3)
		body = emitHandleResultErr(body, buildIoErr, allocBox, 3, 5, 6)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstCall(body, streamDrop)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI64Store(body, 3, readerPosOff)

	body = emitHandleResultOkI64(body, allocBox, 9, 6)

	return inst.PutFunctionBody(nil, putLocalsI32I64(6, 1), body)
}

// emitReaderAdvanceP2 appends `mem64[r + pos] += n` for a preview-2
// Reader, `n` being the i32 byte count in `nLocal`: the read bodies call
// it so a later SEEK_CUR knows where the stream is.
func emitReaderAdvanceP2(body []byte, rLocal, nLocal uint32) []byte {
	body = inst.InstLocalGet(body, rLocal)
	body = inst.InstLocalGet(body, rLocal)
	body = memory.InstI64Load(body, 3, readerPosOff)
	body = inst.InstLocalGet(body, nLocal)
	body = convert.InstI64ExtendI32U(body)
	body = numeric.InstI64Add(body)
	return memory.InstI64Store(body, 3, readerPosOff)
}

// emitReaderBoxP2 appends the allocation of a preview-2 Reader — rc
// sentinel, then {stream, descriptor, pos = 0} — with the stream and
// descriptor taken from the two locals, leaving the data pointer in
// `dataLocal`.
func emitReaderBoxP2(body []byte, alloc, streamLocal, descLocal, dataLocal uint32) []byte {
	body = inst.InstI32Const(body, 24)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, dataLocal)
	body = inst.InstI32Const(body, -0x80000000) // static rc sentinel
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, dataLocal) // data pointer = base + 8
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstLocalGet(body, streamLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstLocalGet(body, descLocal)
	body = memory.InstI32Store(body, 2, 4)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstI64Const(body, 0)
	return memory.InstI64Store(body, 3, readerPosOff)
}
