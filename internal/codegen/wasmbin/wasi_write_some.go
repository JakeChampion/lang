package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// `w.write_some(s)` — one write and the COUNT it returned, where
// `w.write(s)` is the same call in a loop and can only say whether the
// whole string landed. The count is output for GNU `shred`'s failing-write
// offset and for `dd`'s record tally (#9231).
//
// Zero is a real answer rather than an error on both previews: a stream
// with no room for a byte right now has written none of them.

// buildWriterWriteSomeBody is the preview-1 body, where a handle IS its
// fd: one `fd_write` of the whole string, and the `nwritten` the host
// reports back.
//
// Signature: (w, s_data, s_len) → i32 — heap-form Result[i64, IoError].
//
// Locals after the three params:
//
//	3: $scratch  4: $fd  5: $errno  6: $errptr  7: $box
//	8: $buf  9: $byte_len  10: $norm  11: $nwritten
func buildWriterWriteSomeBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	fdWrite := idxs["wasi_fd_write"]

	var body []byte
	// scratch: iov.base at +0, iov.len at +4, the nwritten retptr at +8.
	body = inst.InstI32Const(body, writerScratchAddr)
	body = inst.InstLocalSet(body, 3)
	// fd = mem[w]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)
	// The bytes, normalized out of the SSO form into a heap buffer.
	body = emitStrNormalize(body, idxs, 1, 2, 8, 9, 10)

	// iov.base = buf
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)
	// iov.len = byte_len
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI32Store(body, 2, 0)

	// fd_write(fd, scratch, 1, scratch+8)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstLocalTee(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleResultErr(body, buildIoErr, allocRc1, 5, 6, 7)
	}
	body = inst.InstEnd(body)

	// nwritten = mem[scratch+8]
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 11)
	body = emitHandleResultOkI64U(body, allocRc1, 11, 7)

	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriterWriteSomeBodyP2 is the preview-2 body. A Writer there holds
// an output STREAM, and `blocking-write-and-flush` either takes the whole
// chunk it is handed or fails — there is no partial answer to report — so
// "some" is one chunk of at most 4096 bytes, the same bound
// `buildWriterWriteBodyP2` chunks its loop at. A caller that loops on the
// count therefore makes the same progress it would on preview 1.
//
// Locals after the three params:
//
//	3: $rb  4: $handle  5: $buf  6: $byte_len  7: $chunk
//	8: $errptr  9: $box  10: $norm
func buildWriterWriteSomeBodyP2(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	blockingWrite := idxs["wasi_blocking_write_and_flush_p2"]

	var body []byte
	body = inst.InstI32Const(body, writerScratchAddr)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)
	body = emitStrNormalize(body, idxs, 1, 2, 5, 6, 10)

	// chunk = min(byte_len, 4096)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalSet(body, 7)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 4096)
	body = numeric.InstI32GtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4096)
		body = inst.InstLocalSet(body, 7)
	}
	body = inst.InstEnd(body)

	// Nothing asked for is nothing written, and no call to make.
	body = inst.InstLocalGet(body, 7)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleResultOkI64U(body, allocRc1, 7, 9)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// blocking-write-and-flush(handle, buf, chunk, rb)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, blockingWrite)
	// A stream-error has no errno to report, so it is classified against
	// the generic one, as the write loop beside it does.
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 8)
		body = emitHandleResultErr(body, buildIoErr, allocRc1, 8, 8, 9)
	}
	body = inst.InstEnd(body)

	// The bytes landed, so the offset a later seek reports moves with
	// them (wasi_writer_seek.go).
	body = emitWriterAdvanceP2(body, 0, 7)
	body = emitHandleResultOkI64U(body, allocRc1, 7, 9)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
