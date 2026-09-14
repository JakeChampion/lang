package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// `r.flags()` / `w.flags()` on both previews — the handle's open flags as
// Fern's own three bits: 1 readable, 2 writable, 4 appending.
//
// The two previews answer from different places, and neither is fcntl.
const (
	// fernFlagRead / Write / Append are the bits the checker's contract
	// names. Nothing else is reported: O_NONBLOCK is the flag a caller
	// asks for next and preview 2 has no spelling for it at all, so a
	// cleared bit there would be a claim nobody measured.
	fernFlagRead   = 1
	fernFlagWrite  = 2
	fernFlagAppend = 4

	// The preview-1 fdstat record, whose layout memlayout.go reserves
	// fdstatBufAddr for: fs_filetype at 0, fs_flags at 2,
	// fs_rights_base at 8.
	fdstatFlagsOff  = 2
	fdstatRightsOff = 8
	// fdflagsAppend is preview 1's own APPEND bit in fs_flags, and the
	// two rights a handle needs to be readable or writable.
	fdflagsAppend = 1
	rightsFdRead  = 1 << 1
	rightsFdWrite = 1 << 6
)

// buildFdFlagsBody assembles __fern_fd_flags on preview 1, where a handle
// IS its fd, so one body serves both methods.
//
// Signature: (h) → i32 — heap-form Result[i64, IoError]. One
// fd_fdstat_get, and the three bits come out of the record it fills: the
// two rights say whether the descriptor may be read and written, and
// fs_flags carries APPEND. That is preview 1's whole answer to the
// question fcntl(F_GETFL) answers on a native, and the mapping is exact
// rather than a reduction — the record has no access MODE to collapse.
//
// Locals after the param:
//
//	1: $errno  2: $bits  3: $errptr  4: $box
func buildFdFlagsBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	fdstatGet := idxs["wasi_fd_fdstat_get"]

	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, fdstatBufAddr)
	body = inst.InstCall(body, fdstatGet)
	body = inst.InstLocalSet(body, 1)

	body = inst.InstLocalGet(body, 1)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleResultErr(body, buildIoErr, allocRc1, 1, 3, 4)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 2)
	body = emitFdstatBit(body, 2, fdstatRightsOff, rightsFdRead, fernFlagRead)
	body = emitFdstatBit(body, 2, fdstatRightsOff, rightsFdWrite, fernFlagWrite)
	body = emitFdstatFlagBit(body, 2, fdflagsAppend, fernFlagAppend)

	body = emitFlagsOk(body, allocRc1, 2, 4)
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitFdstatBit ORs `out` with `fernBit` when `right` is set in the rights
// word at `off` in the fdstat record. The word is a u64 and the load is an
// i32: both rights this asks about live in its LOW half — FD_READ is bit 1
// and FD_WRITE bit 6 — so half of it is all that has to be read.
func emitFdstatBit(body []byte, outLocal uint32, off uint32, right int32, fernBit int32) []byte {
	body = inst.InstI32Const(body, fdstatBufAddr)
	body = memory.InstI32Load(body, 2, off)
	body = inst.InstI32Const(body, right)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, outLocal)
		body = inst.InstI32Const(body, fernBit)
		body = numeric.InstI32Or(body)
		body = inst.InstLocalSet(body, outLocal)
	}
	body = inst.InstEnd(body)
	return body
}

// emitFdstatFlagBit is the same for a bit of the 16-bit fs_flags word.
func emitFdstatFlagBit(body []byte, outLocal uint32, flag int32, fernBit int32) []byte {
	body = inst.InstI32Const(body, fdstatBufAddr)
	body = memory.InstI32Load16U(body, 1, fdstatFlagsOff)
	body = inst.InstI32Const(body, flag)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, outLocal)
		body = inst.InstI32Const(body, fernBit)
		body = numeric.InstI32Or(body)
		body = inst.InstLocalSet(body, outLocal)
	}
	body = inst.InstEnd(body)
	return body
}

// emitFlagsOk wraps the i32 bits in Ok as the i64 payload the Result box
// carries, and returns the box.
func emitFlagsOk(body []byte, allocRc1, bitsLocal, boxLocal uint32) []byte {
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag = Ok
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstLocalGet(body, bitsLocal)
	body = convert.InstI64ExtendI32U(body)
	body = memory.InstI64Store(body, 3, 8)
	return inst.InstLocalGet(body, boxLocal)
}

// buildReaderFlagsBodyP2 is the preview-2 Reader answer, and
// buildWriterFlagsBodyP2 the Writer's. Neither asks the host anything.
//
// A component has no fd table and no fcntl, and `descriptor.get-flags`
// would answer for the DESCRIPTOR — not for the handle, which is what
// was asked about: a stdio handle owns no descriptor at all (the same
// fact that makes `stat` answer the all-zero record there), and the
// append-ness of a Writer is a property of the STREAM, since preview 2
// has no append bit on a descriptor and an appending Writer is one that
// was opened `append-via-stream`.
//
// So both answer from the handle's own construction, which is where the
// facts live on this target: a Reader holds an input-stream and is
// readable, a Writer holds an output-stream and is writable, and the
// Writer box carries the append flag `Writer.seek` already keeps. Every
// answer is therefore exactly as true as the handle is — what it does
// NOT report is a parent's flags on an inherited stdio handle, which no
// preview-2 interface exposes.
func buildReaderFlagsBodyP2(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, fernFlagRead)
	return finishFlagsP2(body, idxs["__fern_alloc_rc1"])
}

func buildWriterFlagsBodyP2(idxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, fernFlagWrite)
	// | (append ? 4 : 0), from the box.
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, writerAppendOff)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstI32Const(body, fernFlagAppend)
	body = inst.InstElse(body)
	body = inst.InstI32Const(body, 0)
	body = inst.InstEnd(body)
	body = numeric.InstI32Or(body)
	return finishFlagsP2(body, idxs["__fern_alloc_rc1"])
}

// finishFlagsP2 takes a stack holding the flag word and builds Ok of it.
//
// Locals after the param: 1: $bits  2: $box
func finishFlagsP2(body []byte, allocRc1 uint32) []byte {
	body = inst.InstLocalSet(body, 1)
	body = emitFlagsOk(body, allocRc1, 1, 2)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
