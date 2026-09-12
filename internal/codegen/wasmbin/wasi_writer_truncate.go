// `Writer.truncate` for the wasmbin backend.
//
// The DESCRIPTOR form of the resize, beside the path form in
// wasi_truncate.go. Both previews already import what it needs, because
// the path form had to borrow it: neither preview has a path-based
// set-size, so `truncate` opens a descriptor of its own and calls the
// same `fd_filestat_set_size` / `descriptor.set-size` this does. Here the
// descriptor is the Writer's own and the helper neither opens nor drops
// it.
//
// A preview-2 handle whose stream was not opened from a descriptor — a
// stdio Writer — has nothing to resize and answers ENOTSUP, the same
// refusal `Writer.stat` gives there.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// emitHandleOptionSome appends "classify `errnoLocal` against an empty
// path, wrap it in Some, return the box" — emitHandleResultErr's Option
// sibling, where the carrying variant is tag 0 rather than tag 1.
func emitHandleOptionSome(body []byte, buildIoErr, allocRc1, errnoLocal, errPtrLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, errPtrLocal)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag = Some
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, errPtrLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	return inst.InstReturn(body)
}

// buildWriterTruncateBody assembles __fern_writer_truncate on preview 1.
//
// Signature: (w, length: i64) → i32 — heap-form Option[IoError]. One
// fd_filestat_set_size on the handle's own fd; no return area, since the
// call answers with an errno alone.
//
// Locals after the two params:
//
//	2: $errno  3: $err_ptr  4: $box
func buildWriterTruncateBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	setSize := idxs["wasi_fd_filestat_set_size"]

	var body []byte
	// errno = fd_filestat_set_size(mem[w], length)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, setSize)
	body = inst.InstLocalSet(body, 2)

	body = inst.InstLocalGet(body, 2)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 2, 3, 4)
	}
	body = inst.InstEnd(body)

	body = emitPayloadlessResultBox(body, allocRc1, 4, 8, 1) // None

	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriterTruncateBodyP2 is the preview-2 __fern_writer_truncate:
// descriptor.set-size on the descriptor the handle was opened on. A
// stdio handle owns no descriptor and answers Unsupported, as
// `Writer.stat` does.
//
// Locals after the two params:
//
//	2: $rb  3: $desc  4: $errno  5: $err_ptr  6: $box
func buildWriterTruncateBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	setSize := idxs["wasi_descriptor_set_size_p2"]

	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstI32Const(body, noDescriptor)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, errnoNoTsup)
		body = inst.InstLocalSet(body, 4)
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 4, 5, 6)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, setSize)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 2, 4, emptyOkErrorCodeOff)
		body = emitHandleOptionSome(body, buildIoErr, allocRc1, 4, 5, 6)
	}
	body = inst.InstEnd(body)

	body = emitPayloadlessResultBox(body, allocRc1, 6, 8, 1) // None

	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
