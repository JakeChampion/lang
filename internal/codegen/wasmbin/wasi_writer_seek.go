// `Writer.seek` for the wasmbin backend's preview-2 world.
//
// Preview 1 has none of this: a handle there IS its fd, so a Writer's
// seek is `fd_seek` on it and the Reader's body serves both (runtime.go
// points __fern_writer_seek at buildReaderSeekBody). Preview 2 has no
// file offset at all — an output-stream writes where it was opened — so
// the offset lives in the Writer and a seek reopens the stream at the
// target, the write-side mirror of buildReaderSeekBodyP2.
//
// What the kernel does, and what this matches (measured on Linux):
//
//   - a plain Writer's offset starts at 0 and advances with every byte
//     written, so SEEK_CUR answers from the recorded position;
//   - an APPEND Writer's offset is 0 until something is written and the
//     file's end afterwards, because each write seeks there atomically;
//     a seek on one moves the offset and the writes keep landing at the
//     end, which is why the stream is left alone here;
//   - a target past the end is legal (the file grows a hole on the next
//     write), a negative one is EINVAL, and a handle with no descriptor
//     — a stdio Writer, a pipe — is ESPIPE, which is the answer a pipe
//     gives whatever the whence.
package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// Offsets into a preview-2 Writer, from its data pointer. `stream` and
// `descriptor` are at 0 and 4 as every other Writer body reads them;
// the three that follow are this file's.
const (
	writerPosOff    uint32 = 8  // i64 — the emulated file offset
	writerAppendOff uint32 = 16 // i32 — opened append-via-stream
	writerWroteOff  uint32 = 20 // i32 — an append Writer has written since its last seek
)

// writerBoxBytes is the allocation behind a preview-2 Writer: the rc
// header plus the five fields above. Every constructor uses it, so the
// seek body can count on the offsets being there.
const writerBoxBytes int32 = 8 + 24

// emitWriterFieldsP2 appends the initialisation of the three seek
// fields on a Writer whose data pointer is in `wLocal`: offset 0, and
// `append` as given. A fresh handle has written nothing.
func emitWriterFieldsP2(body []byte, wLocal uint32, appendStream bool) []byte {
	body = inst.InstLocalGet(body, wLocal)
	body = inst.InstI64Const(body, 0)
	body = memory.InstI64Store(body, 3, writerPosOff)
	body = inst.InstLocalGet(body, wLocal)
	body = inst.InstI32Const(body, boolConst(appendStream))
	body = memory.InstI32Store(body, 2, writerAppendOff)
	body = inst.InstLocalGet(body, wLocal)
	body = inst.InstI32Const(body, 0)
	return memory.InstI32Store(body, 2, writerWroteOff)
}

func boolConst(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// emitWriterAdvanceP2 appends "the Writer at `wLocal` wrote the `n`
// bytes in `nLocal`": the offset moves, and an append Writer records
// that its offset is now the file's end rather than that count.
func emitWriterAdvanceP2(body []byte, wLocal, nLocal uint32) []byte {
	body = inst.InstLocalGet(body, wLocal)
	body = inst.InstLocalGet(body, wLocal)
	body = memory.InstI64Load(body, 3, writerPosOff)
	body = inst.InstLocalGet(body, nLocal)
	body = convert.InstI64ExtendI32U(body)
	body = numeric.InstI64Add(body)
	body = memory.InstI64Store(body, 3, writerPosOff)
	body = inst.InstLocalGet(body, wLocal)
	body = inst.InstI32Const(body, 1)
	return memory.InstI32Store(body, 2, writerWroteOff)
}

// emitDescSizeP2 appends "the size of the descriptor in `descLocal`,
// into `sizeLocal`" through descriptor.stat, returning the Err box that
// `onErr` builds when the host refuses. `rbLocal` is scratch.
func emitDescSizeP2(body []byte, idxs map[string]uint32, descLocal, rbLocal, errnoLocal, sizeLocal uint32, onErr func([]byte) []byte) []byte {
	alloc := idxs["__fern_alloc"]
	descStat := idxs["wasi_descriptor_stat_p2"]
	body = inst.InstI32Const(body, statAtRetBytes)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, rbLocal)
	body = inst.InstLocalGet(body, descLocal)
	body = inst.InstLocalGet(body, rbLocal)
	body = inst.InstCall(body, descStat)
	body = inst.InstLocalGet(body, rbLocal)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, rbLocal, errnoLocal, statAtTypeOff)
		body = onErr(body)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, rbLocal)
	body = memory.InstI64Load(body, 3, statAtSizeOff)
	return inst.InstLocalSet(body, sizeLocal)
}

// buildWriterSeekBodyP2 is the preview-2 __fern_writer_seek.
//
// Locals after the three params:
//
//	i32 — 3: $errno  4: $rb  5: $errptr  6: $box  7: $desc  8: $stream
//	i64 — 9: $target  10: $cur
func buildWriterSeekBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	writeVia := idxs["wasi_descriptor_write_via_stream_p2"]
	streamDrop := idxs["wasi_io_output_stream_drop"]

	onErr := func(b []byte) []byte {
		return emitHandleResultErr(b, buildIoErr, allocRc1, 3, 5, 6)
	}
	refuse := func(b []byte, errno int32) []byte {
		b = inst.InstI32Const(b, errno)
		b = inst.InstLocalSet(b, 3)
		return onErr(b)
	}

	var body []byte
	// The whence is checked before the handle is: `lseek(pipe, 0, 5)` is
	// EINVAL where `lseek(pipe, 0, 0)` is ESPIPE.
	body = emitWhenceGuardP2(body, idxs, 2, 3, 5, 6)
	// A handle with no descriptor has no offset to move: ESPIPE, the
	// same refusal lseek gives on a pipe.
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalTee(body, 7)
	body = inst.InstI32Const(body, noDescriptor)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = refuse(body, errnoSpipe)
	}
	body = inst.InstEnd(body)

	// cur = the offset the kernel would report: the recorded one, or the
	// file's end for an append Writer that has written since its last seek.
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, writerPosOff)
	body = inst.InstLocalSet(body, 10)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, writerAppendOff)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, writerWroteOff)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitDescSizeP2(body, idxs, 7, 4, 3, 10, onErr)
	}
	body = inst.InstEnd(body)

	// target = offset, then whence: SEEK_CUR from cur, SEEK_END from the size.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalSet(body, 9)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 10)
		body = numeric.InstI64Add(body)
		body = inst.InstLocalSet(body, 9)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 2)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitDescSizeP2(body, idxs, 7, 4, 3, 10, onErr)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 10)
		body = numeric.InstI64Add(body)
		body = inst.InstLocalSet(body, 9)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI64Const(body, 0)
	body = numeric.InstI64LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = refuse(body, errnoInval)
	}
	body = inst.InstEnd(body)

	// An append Writer's writes keep landing at the end whatever the
	// offset says, so its stream is left as it is; every other Writer
	// gets a fresh write-via-stream at the target, opened before the old
	// one is dropped so a refusal leaves the handle as it was.
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, writerAppendOff)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, writeVia)
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = appendErrnoFromErrorCode(body, idxs, 4, 3)
			body = onErr(body)
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
	}
	body = inst.InstEnd(body)

	// The offset is the target now, and nothing has been written at it.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI64Store(body, 3, writerPosOff)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, writerWroteOff)
	body = emitHandleResultOkI64(body, allocRc1, 9, 6)

	return inst.PutFunctionBody(nil, putLocalsI32I64(6, 2), body)
}
