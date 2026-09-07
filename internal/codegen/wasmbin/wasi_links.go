// The directory and link primitives for the wasmbin backend:
// `create_dir`, `remove_dir`, `create_link`, `create_symlink` and
// `read_link` (#8883).
//
// Split from wasi_fs_dir.go, which owns the family these five joined:
// they share its `preopenDirfd`, `emitStrNormalize`, `__build_io_error`
// and result-box shapes and add nothing of their own beyond a second
// path operand.
//
// Each is a single WASI call, which is why they are here at all rather
// than refused on this target. Preview 1 has `path_create_directory`,
// `path_remove_directory`, `path_link`, `path_symlink` and
// `path_readlink`, and preview 2 has the descriptor methods that match
// them one for one.
//
// Two honest gaps, both properties of WASI rather than of this code and
// both recorded in docs/FREESTANDING-CORE.md:
//
//   - `path_create_directory` carries NO mode. The `mode` argument is
//     accepted and dropped here, so a directory a component creates has
//     whatever mode the host chooses. `umask` is refused outright on
//     this target (E066, capability `fsmode`) rather than answering a
//     mask that describes nothing.
//   - Every path is resolved under the first preopen, so a path that
//     escapes it — an absolute one, or one that climbs out with `..` —
//     is ENOTCAPABLE where a kernel would resolve it. That is the same
//     bound the rest of the family already carries.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// emitResultErrFor is emitResultErr with the path the IoError names
// given explicitly, rather than taken from params 0 and 1.
//
// The two-operand helpers need it: `create_link` and `create_symlink`
// report the LINK they failed to create, not the file it was to point
// at, because that is the operand `ln` and `link` name in their
// diagnostics.
func emitResultErrFor(body []byte, buildIoErr, allocRc1, errnoLocal, pathDataLocal, pathLenLocal, errPtrLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstLocalGet(body, pathDataLocal)
	body = inst.InstLocalGet(body, pathLenLocal)
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, errPtrLocal)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 1) // tag = Err
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, errPtrLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstReturn(body)
	return body
}

// ---- preview 1 ------------------------------------------------------

// buildCreateDirBody assembles __fern_create_dir.
//
// Signature: (path_data, path_len, mode) → i32 — heap-form
// Result[void, IoError].
//
// path_create_directory under the fd-3 preopen. ONE level: a missing
// parent is ENOENT and an existing name EEXIST, both reaching the
// caller, which is the whole difference from __fern_create_dir_all. The
// mode is dropped — see this file's header.
//
// Locals after the three params:
//
//	3: $path_buf  4: $path_byte_len  5: $i (normalize scratch)
//	6: $errno     7: $err_ptr        8: $box
func buildCreateDirBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	mkdir := idxs["wasi_path_create_directory"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 5)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, mkdir)
	body = inst.InstLocalSet(body, 6)

	body = inst.InstLocalGet(body, 6)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 6, 0, 1, 7, 8)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7)
	body = emitResultOkPtr(body, allocRc1, 7, 8)

	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRemoveDirBody assembles __fern_remove_dir.
//
// Signature: (path_data, path_len) → i32 — heap-form Result[void,
// IoError].
//
// path_remove_directory under the fd-3 preopen. A non-empty directory
// is ENOTEMPTY and reaches the caller, which is what separates this
// from remove_dir_all's drain.
//
// Locals after the two params:
//
//	2: $path_buf  3: $path_byte_len  4: $i (normalize scratch)
//	5: $errno     6: $err_ptr        7: $box
func buildRemoveDirBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	rmdir := idxs["wasi_path_remove_directory"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, rmdir)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 5, 0, 1, 6, 7)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = emitResultOkPtr(body, allocRc1, 6, 7)

	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildCreateLinkBody assembles __fern_create_link.
//
// Signature: (target_data, target_len, path_data, path_len) → i32 —
// heap-form Result[void, IoError].
//
// path_link(preopen, 0, target, preopen, path). The old_flags word is 0
// rather than SYMLINK_FOLLOW, so a symlink named as the target is
// linked to itself — what link(1) and ln(1) without -L do.
//
// Locals after the four params:
//
//	4: $target_buf  5: $target_byte_len  6: $path_buf
//	7: $path_byte_len  8: $i (normalize scratch)
//	9: $errno  10: $err_ptr  11: $box
func buildCreateLinkBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	link := idxs["wasi_path_link"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 8)
	body = emitStrNormalize(body, idxs, 2, 3, 6, 7, 8)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 0) // old_flags: no SYMLINK_FOLLOW
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, link)
	body = inst.InstLocalSet(body, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 9, 2, 3, 10, 11)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10)
	body = emitResultOkPtr(body, allocRc1, 10, 11)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildCreateSymlinkBody assembles __fern_create_symlink.
//
// Signature: (target_data, target_len, path_data, path_len) → i32 —
// heap-form Result[void, IoError].
//
// path_symlink(target, preopen, path). The target is stored verbatim
// and never resolved, so a link to something that does not exist is
// created without complaint.
//
// Locals: buildCreateLinkBody's exactly.
func buildCreateSymlinkBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	symlink := idxs["wasi_path_symlink"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 8)
	body = emitStrNormalize(body, idxs, 2, 3, 6, 7, 8)

	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, symlink)
	body = inst.InstLocalSet(body, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 9, 2, 3, 10, 11)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10)
	body = emitResultOkPtr(body, allocRc1, 10, 11)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// readLinkBufBytes is the buffer path_readlink writes the target into.
// PATH_MAX is the kernel bound on a stored target, and WASI's own
// hosts inherit it, so a target that fills this buffer is longer than
// any path can be.
const readLinkBufBytes = 4096

// buildReadLinkBody assembles __fern_read_link.
//
// Signature: (path_data, path_len) → i32 — heap-form Result[string,
// IoError].
//
// path_readlink(preopen, path, buf, 4096, used_ptr). WASI reports how
// many bytes it wrote, and — like readlink(2) — truncates silently when
// the buffer is too small, so a FULL buffer is answered with
// ENAMETOOLONG rather than a target that is missing its tail.
//
// Locals after the two params:
//
//	2: $rb (used_ptr)  3: $path_buf  4: $path_byte_len
//	5: $i (normalize scratch)  6: $errno  7: $err_ptr  8: $box
//	9: $buf  10: $used  11: $target
func buildReadLinkBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	readlink := idxs["wasi_path_readlink"]

	var body []byte
	// The used-count cell first, so it stays 4-byte aligned before the
	// normalize allocations move the bump cursor by an arbitrary count.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 5)

	body = inst.InstI32Const(body, readLinkBufBytes)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 9)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, readLinkBufBytes)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, readlink)
	body = inst.InstLocalSet(body, 6)

	body = inst.InstLocalGet(body, 6)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 6, 0, 1, 7, 8)
	}
	body = inst.InstEnd(body)

	// used = mem[rb]; a full buffer is ENAMETOOLONG (errno 36), not a
	// truncated target.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 10)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, readLinkBufBytes)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 36)
		body = inst.InstLocalSet(body, 6)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 6, 0, 1, 7, 8)
	}
	body = inst.InstEnd(body)

	body = emitReadLinkOk(body, allocRc1, 9, 10, 11, 8)
	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitReadLinkOk appends "copy `used` bytes out of `buf` into an owned
// string and wrap it in Ok", leaving the box on the stack.
//
// The target is copied rather than handed over: `buf` is a 4 KiB block
// whatever the answer's length, and the string the caller keeps must be
// the size of its own contents.
//
// The box is 16 bytes — tag@0, data@8, len@12 — the Result[string,
// IoError] shape read_file already builds.
func emitReadLinkOk(body []byte, allocRc1, bufLocal, usedLocal, targetLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, usedLocal)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalSet(body, targetLocal)
	body = inst.InstLocalGet(body, targetLocal)
	body = inst.InstLocalGet(body, bufLocal)
	body = inst.InstLocalGet(body, usedLocal)
	body = memory.InstMemoryCopy(body)

	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocRc1)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag = Ok
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, targetLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, usedLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	return body
}

// ---- preview 2 ------------------------------------------------------

// buildCreateDirBodyP2 is the preview-2 buildCreateDirBody:
// get-directories → create-directory-at for ONE level. The mode is
// dropped here too — preview 2's method has no mode either.
//
// Locals after the three params:
//
//	3: $rb  4: $path_buf  5: $path_byte_len  6: $preopen
//	7: $errno  8: $err_ptr  9: $box  10: $i
func buildCreateDirBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	mkdir := idxs["wasi_descriptor_create_directory_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 10)
	body = emitPreopenP2(body, alloc, getDirs, 3, 6)

	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, mkdir)

	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 3, 7, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 7, 0, 1, 8, 9)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 8)
	body = emitResultOkPtr(body, allocRc1, 8, 9)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRemoveDirBodyP2 is the preview-2 buildRemoveDirBody:
// get-directories → remove-directory-at.
//
// Locals after the two params:
//
//	2: $rb  3: $path_buf  4: $path_byte_len  5: $preopen
//	6: $errno  7: $err_ptr  8: $box  9: $i
func buildRemoveDirBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	rmdir := idxs["wasi_descriptor_remove_directory_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 9)
	body = emitPreopenP2(body, alloc, getDirs, 2, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, rmdir)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 2, 6, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 6, 0, 1, 7, 8)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7)
	body = emitResultOkPtr(body, allocRc1, 7, 8)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildCreateLinkBodyP2 is the preview-2 buildCreateLinkBody:
// get-directories → link-at, with the old-path-flags word 0 so the
// target's own final symlink is not followed.
//
// Locals after the four params:
//
//	4: $rb  5: $target_buf  6: $target_byte_len  7: $path_buf
//	8: $path_byte_len  9: $preopen  10: $errno  11: $err_ptr
//	12: $box  13: $i
func buildCreateLinkBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	link := idxs["wasi_descriptor_link_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 5, 6, 13)
	body = emitStrNormalize(body, idxs, 2, 3, 7, 8, 13)
	body = emitPreopenP2(body, alloc, getDirs, 4, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 0) // old-path-flags: no symlink-follow
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, link)

	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 4, 10, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 10, 2, 3, 11, 12)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11)
	body = emitResultOkPtr(body, allocRc1, 11, 12)

	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildCreateSymlinkBodyP2 is the preview-2 buildCreateSymlinkBody:
// get-directories → symlink-at. Locals are buildCreateLinkBodyP2's.
func buildCreateSymlinkBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	symlink := idxs["wasi_descriptor_symlink_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 5, 6, 13)
	body = emitStrNormalize(body, idxs, 2, 3, 7, 8, 13)
	body = emitPreopenP2(body, alloc, getDirs, 4, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, symlink)

	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 4, 10, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 10, 2, 3, 11, 12)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11)
	body = emitResultOkPtr(body, allocRc1, 11, 12)

	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadLinkBodyP2 is the preview-2 buildReadLinkBody:
// get-directories → readlink-at.
//
// Preview 2 hands back a `result<string, error-code>` rather than
// filling a caller's buffer, so there is no truncation to detect: the
// ok arm carries a host-allocated (ptr, len) pair at +4 / +8, already
// in this module's memory through cabi_realloc. It is still copied into
// an owned string, because the caller keeps it past this call.
//
// Locals after the two params:
//
//	2: $rb  3: $path_buf  4: $path_byte_len  5: $preopen
//	6: $errno  7: $err_ptr  8: $box  9: $i  10: $used  11: $target
func buildReadLinkBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	readlink := idxs["wasi_descriptor_readlink_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 9)
	body = emitPreopenP2(body, alloc, getDirs, 2, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, readlink)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// A single-word ok payload puts the error-code at +4, not the
		// +1 an empty ok arm uses.
		body = appendErrnoFromErrorCodeAt(body, idxs, 2, 6, 4)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 6, 0, 1, 7, 8)
	}
	body = inst.InstEnd(body)

	// ok: string ptr @ rb+4, len @ rb+8.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstLocalSet(body, 10)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 9)

	body = emitReadLinkOk(body, allocRc1, 9, 10, 11, 8)
	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
