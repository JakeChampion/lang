// The filesystem-metadata primitives for the wasmbin backend: `rename`
// and `set_file_times` (#9059).
//
// The third of the set, `chmod`, is NOT here and its absence is the
// point: neither WASI preview has permission bits on a filesystem
// entry, so the capability `fsmode` is granted by no wasi profile and
// E066 refuses the builtin at check time. There is nothing for a
// backend to emit and nothing to stub.
//
// These two are real WASI calls — `path_rename` and
// `path_filestat_set_times` on preview 1, `descriptor.rename-at` and
// `descriptor.set-times-at` on preview 2 — so they are provided rather
// than refused. They share wasi_fs_dir.go's `preopenDirfd`,
// `emitStrNormalize`, `__build_io_error` and result-box shapes.
//
// Three gaps are WASI's own and belong in docs/FREESTANDING-CORE.md
// rather than in a workaround:
//
//   - **Timestamps are UNSIGNED nanoseconds on both previews** —
//     preview 1's `timestamp` and preview 2's `datetime.seconds`
//     alike — so a second count before 1970 has no representation.
//     A negative one is answered EOVERFLOW rather than wrapped into a
//     date six hundred years out, which is what the unsigned
//     conversion would otherwise produce and present as a success.
//   - **A rename cannot cross preopens.** Both operands resolve under
//     the first one, and the destination descriptor handed to
//     `rename-at` is that same preopen.
//   - Every path resolves under the first preopen, so one that escapes
//     it is ENOTCAPABLE where a kernel would resolve it — the whole
//     `fs` family's bound, not these two's.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// The bits of set_file_times' `flags` word, and the WASI constants they
// translate into. The Fern word is its own: preview 1 spells "leave this
// one alone" as a missing `fstflags` bit and preview 2 as a
// `new-timestamp` discriminant, and neither is a flag the caller could
// have passed through.
const (
	fsMetaNoFollow  = 1
	fsMetaOmitAtime = 2
	fsMetaOmitMtime = 4

	// preview 1 `fstflags`: which of the pair the call writes.
	wasiFstflagsAtim = 1
	wasiFstflagsMtim = 4
	// preview 1 `lookupflags` / preview 2 `path-flags`: follow a final
	// symlink.
	wasiSymlinkFollow = 1
	// preview 2 `new-timestamp` discriminants.
	wasiTimestampNoChange = 0
	wasiTimestampValue    = 2

	// WASI errno `overflow`, the answer for a time the previews cannot
	// represent.
	wasiErrnoOverflow = 61
)

// emitTimesOverflowGuard appends the pre-1970 refusal: for each half of
// the pair the call is actually writing, a negative second count is
// EOVERFLOW rather than an unsigned wrap.
//
// `errnoLocal` is left holding the errno and is scratch either way.
func emitTimesOverflowGuard(body []byte, idxs map[string]uint32, flagsLocal, errnoLocal, errPtrLocal, boxLocal uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, errnoLocal)
	for _, half := range []struct{ omitBit, secParam uint32 }{
		{fsMetaOmitAtime, 2},
		{fsMetaOmitMtime, 4},
	} {
		body = inst.InstLocalGet(body, flagsLocal)
		body = inst.InstI32Const(body, int32(half.omitBit))
		body = numeric.InstI32And(body)
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, half.secParam)
			body = inst.InstI64Const(body, 0)
			body = numeric.InstI64LtS(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstI32Const(body, wasiErrnoOverflow)
				body = inst.InstLocalSet(body, errnoLocal)
			}
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
	}
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, errnoLocal, 0, 1, errPtrLocal, boxLocal)
	}
	body = inst.InstEnd(body)
	return body
}

// emitFlagIf appends `if (flags & bit) { dst = then }`, the shape every
// translation of the Fern flags word takes. `invert` tests the bit CLEAR
// instead.
func emitFlagIf(body []byte, flagsLocal uint32, bit int32, invert bool, dst uint32, then int32) []byte {
	body = inst.InstLocalGet(body, flagsLocal)
	body = inst.InstI32Const(body, bit)
	body = numeric.InstI32And(body)
	if invert {
		body = numeric.InstI32Eqz(body)
	}
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, then)
	body = inst.InstLocalSet(body, dst)
	body = inst.InstEnd(body)
	return body
}

// emitNanosSince appends `sec * 1e9 + nsec` as one i64, the single
// nanosecond count preview 1's `timestamp` is.
func emitNanosSince(body []byte, secParam, nsecParam uint32) []byte {
	body = inst.InstLocalGet(body, secParam)
	body = inst.InstI64Const(body, nsPerSecond)
	body = numeric.InstI64Mul(body)
	body = inst.InstLocalGet(body, nsecParam)
	body = numeric.InstI64Add(body)
	return body
}

// ---- preview 1 ------------------------------------------------------

// buildRenameBody assembles __fern_rename.
//
// Signature: (from_data, from_len, to_data, to_len) → i32 — heap-form
// Result[void, IoError].
//
// path_rename under the fd-3 preopen on both sides. An existing
// destination is replaced, and nothing is copied.
//
// Locals after the four params:
//
//	4: $from_buf  5: $from_byte_len  6: $to_buf  7: $to_byte_len
//	8: $i (normalize scratch)  9: $errno  10: $err_ptr  11: $box
func buildRenameBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	rename := idxs["wasi_path_rename"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 4, 5, 8)
	body = emitStrNormalize(body, idxs, 2, 3, 6, 7, 8)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, rename)
	body = inst.InstLocalSet(body, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// The IoError names the DESTINATION: a rename fails for
		// what is already there, or for the filesystem it was
		// going to.
		body = emitResultErrFor(body, buildIoErr, allocRc1, 9, 2, 3, 10, 11)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10)
	body = emitResultOkPtr(body, allocRc1, 10, 11)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildSetFileTimesBody assembles __fern_set_file_times.
//
// Signature: (path_data, path_len, atime_sec: i64, atime_nsec: i64,
// mtime_sec: i64, mtime_nsec: i64, flags) → i32 — heap-form
// Result[void, IoError].
//
// path_filestat_set_times under the fd-3 preopen. The pair arrives as
// seconds and nanoseconds — the shape `stat` answers in — and is folded
// into the single nanosecond count preview 1 wants; an omitted half is
// a cleared `fstflags` bit, after which its value is not read.
//
// Locals after the seven params:
//
//	7: $path_buf  8: $path_byte_len  9: $i (normalize scratch)
//	10: $errno  11: $err_ptr  12: $box  13: $fstflags  14: $lookupflags
//	15: the MTIM half of $fstflags, before the two are joined
func buildSetFileTimesBody(idxs map[string]uint32) []byte {
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	setTimes := idxs["wasi_path_filestat_set_times"]

	var body []byte
	body = emitTimesOverflowGuard(body, idxs, 6, 10, 11, 12)
	body = emitStrNormalize(body, idxs, 0, 1, 7, 8, 9)

	// fstflags names which HALF of the pair the call writes; an
	// omitted one simply is not named.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 13)
	body = emitFlagIf(body, 6, fsMetaOmitAtime, true, 13, wasiFstflagsAtim)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 15)
	body = emitFlagIf(body, 6, fsMetaOmitMtime, true, 15, wasiFstflagsMtim)
	body = inst.InstLocalGet(body, 13)
	body = inst.InstLocalGet(body, 15)
	body = numeric.InstI32Or(body)
	body = inst.InstLocalSet(body, 13)

	body = inst.InstI32Const(body, wasiSymlinkFollow)
	body = inst.InstLocalSet(body, 14)
	body = emitFlagIf(body, 6, fsMetaNoFollow, false, 14, 0)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 14)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 8)
	body = emitNanosSince(body, 2, 3)
	body = emitNanosSince(body, 4, 5)
	body = inst.InstLocalGet(body, 13)
	body = inst.InstCall(body, setTimes)
	body = inst.InstLocalSet(body, 10)

	body = inst.InstLocalGet(body, 10)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErrFor(body, buildIoErr, allocRc1, 10, 0, 1, 11, 12)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11)
	body = emitResultOkPtr(body, allocRc1, 11, 12)

	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// ---- preview 2 ------------------------------------------------------

// buildRenameBodyP2 is the preview-2 buildRenameBody: get-directories →
// rename-at, with the preopen as both the source and the destination
// descriptor. A rename cannot cross preopens here.
//
// Locals after the four params:
//
//	4: $rb  5: $from_buf  6: $from_byte_len  7: $to_buf
//	8: $to_byte_len  9: $preopen  10: $errno  11: $err_ptr
//	12: $box  13: $i
func buildRenameBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	rename := idxs["wasi_descriptor_rename_at_p2"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 5, 6, 13)
	body = emitStrNormalize(body, idxs, 2, 3, 7, 8, 13)
	body = emitPreopenP2(body, alloc, getDirs, 4, 9)

	body = inst.InstLocalGet(body, 9)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 9) // the new-descriptor borrow
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, rename)

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

// buildSetFileTimesBodyP2 is the preview-2 buildSetFileTimesBody:
// get-directories → set-times-at.
//
// Preview 2 says "leave this one alone" with a `new-timestamp`
// discriminant rather than a flags bit, and its `datetime` is the pair
// this builtin already takes — so the seconds go across untouched and
// only the nanoseconds narrow to the u32 the record declares.
//
// Locals after the seven params:
//
//	7: $rb  8: $path_buf  9: $path_byte_len  10: $preopen
//	11: $errno  12: $err_ptr  13: $box  14: $i  15: $atime_disc
//	16: $mtime_disc  17: $path_flags
func buildSetFileTimesBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocRc1 := idxs["__fern_alloc_rc1"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	setTimes := idxs["wasi_descriptor_set_times_at_p2"]

	var body []byte
	body = emitTimesOverflowGuard(body, idxs, 6, 11, 12, 13)
	body = emitStrNormalize(body, idxs, 0, 1, 8, 9, 14)
	body = emitPreopenP2(body, alloc, getDirs, 7, 10)

	body = inst.InstI32Const(body, wasiTimestampValue)
	body = inst.InstLocalSet(body, 15)
	body = emitFlagIf(body, 6, fsMetaOmitAtime, false, 15, wasiTimestampNoChange)
	body = inst.InstI32Const(body, wasiTimestampValue)
	body = inst.InstLocalSet(body, 16)
	body = emitFlagIf(body, 6, fsMetaOmitMtime, false, 16, wasiTimestampNoChange)
	body = inst.InstI32Const(body, wasiSymlinkFollow)
	body = inst.InstLocalSet(body, 17)
	body = emitFlagIf(body, 6, fsMetaNoFollow, false, 17, 0)

	body = inst.InstLocalGet(body, 10)
	body = inst.InstLocalGet(body, 17)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstLocalGet(body, 15)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = convert.InstI32WrapI64(body)
	body = inst.InstLocalGet(body, 16)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = convert.InstI32WrapI64(body)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, setTimes)

	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCodeAt(body, idxs, 7, 11, emptyOkErrorCodeOff)
		body = emitResultErrFor(body, buildIoErr, allocRc1, 11, 0, 1, 12, 13)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 12)
	body = emitResultOkPtr(body, allocRc1, 12, 13)

	locals := inst.PutLocalsOneGroup(nil, 11, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
