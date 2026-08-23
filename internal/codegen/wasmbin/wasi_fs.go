// File-I/O runtime helpers for the wasmbin backend.
//
// The user-facing `read_file(path)` / `write_file(path, content)`
// builtins lower to `OpCallDirect` with the bare names; the IR
// alias table routes those to the synthetic helpers in this file.
// Both helpers go through WASI preview-1 (`path_open`, `fd_read`,
// `fd_write`, `fd_close`), which the preview-1-to-preview-2
// adapter wraps when the module is composed into a component.
//
// Mirrors the WAT path's read_file / write_file emission;
// the WAT side uses preview-2 streams directly (open-at +
// read-via-stream), which is more efficient at the host
// boundary but requires another ~15 WASI imports per side.
// The preview-1 route here is the smaller wedge as wasmbin
// catches up to WAT parity — see the WAT-retirement PR thread
// for the staging plan.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// preopenDirfd is the WASI preview-1 file descriptor wasmtime
// assigns to the first `--dir` argument: the "main" preopen
// directory the user passed on the command line. Tests use
// `wasmtime run --dir=. PROG` so the program sees its working
// directory at fd 3. Hard-coding this matches the WAT path's
// `$__preopen_dir` cache (which also assumes a single preopen
// at fd 3 on first call and the WAT helper only enumerates as
// a defensive fallback).
const preopenDirfd = 3

// IoError variant tags, mirroring the auto-injected enum in
// `internal/checker/checker.go`. The runtime helpers construct
// IoError values directly in memory, so these need to stay in
// sync with the checker's variant ordering.
const (
	ioErrTagNotFound         int32 = 0
	ioErrTagPermissionDenied int32 = 1
	ioErrTagAlreadyExists    int32 = 2
	ioErrTagInvalidUtf8      int32 = 3
	ioErrTagInterrupted      int32 = 4
	ioErrTagUnsupported      int32 = 5
	ioErrTagOther            int32 = 6
)

// WASI preview-1 errno values used by the file-I/O helpers. The
// full table is in wasi-libc / the wasi spec; we only translate
// the ones with corresponding IoError variants.
const (
	errnoSuccess int32 = 0
	errnoAccess  int32 = 2  // EACCES → PermissionDenied
	errnoExist   int32 = 20 // EEXIST → AlreadyExists
	errnoIlseq   int32 = 25 // EILSEQ → InvalidUtf8 (synthetic — raised by __fern_utf8_valid, not the host)
	errnoIntr    int32 = 27 // EINTR  → Interrupted
	errnoNoEnt   int32 = 44 // ENOENT → NotFound
	errnoNoTsup  int32 = 58 // ENOTSUP → Unsupported
)

// WASI preview-1 RIGHTS bitset values. We only need
// RIGHT_FD_READ (and inheriting variant) for read_file; write
// support adds RIGHT_FD_WRITE later.
const (
	wasiRightFdRead    int64 = 0x02
	wasiRightFdSeek    int64 = 0x01
	wasiRightFdWrite   int64 = 0x40
	wasiRightPathOpen  int64 = 0x2000
	wasiRightFdAllRead       = wasiRightFdRead | wasiRightFdSeek | wasiRightPathOpen
)

// WASI preview-1 `oflags` bits for path_open.
const (
	wasiOflagCreate   int32 = 0x01
	wasiOflagTruncate int32 = 0x08
)

// WASI preview-1 `fdflags` bits for path_open.
const (
	wasiFdflagAppend int32 = 0x01
)

// buildBuildIoErrorBody assembles __build_io_error.
//
// Signature: (errno, path_data, path_len) → i32 (heap-form
// IoError pointer).
//
// Maps a small set of preview-1 errnos to the matching
// IoError variant; everything else lands in
// `Other(path, "io error")`. The variant struct layout is
// (tag@0 + padding + payload@8); single-string payloads
// occupy 8 bytes (data@8, len@12) for 16 bytes total;
// two-string Other occupies 16 bytes (data@8/len@12 +
// data@16/len@20) for 24 bytes total. Payloadless variants
// (Interrupted, Unsupported) are 4 bytes (just the tag).
//
// Locals:
//
//	0: $errno
//	1: $path_data
//	2: $path_len
//	3: $result
func buildBuildIoErrorBody(idxs map[string]uint32) []byte {
	allocBox := idxs["__fern_alloc_box"]
	var body []byte

	// Each errno case: compare, if-then-allocate-and-return the
	// matching variant. Inline rather than table-driven; the set
	// is small and the wasm validator benefits from explicit
	// branch shapes.
	emitSingleStringCase := func(b []byte, errnoVal, tag int32) []byte {
		// if errno == errnoVal { return NotFound|PermissionDenied|... }
		b = inst.InstLocalGet(b, 0) // errno
		b = inst.InstI32Const(b, errnoVal)
		b = numeric.InstI32Eq(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		{
			b = inst.InstI32Const(b, 16) // 16-byte single-string variant
			b = inst.InstCall(b, allocBox)
			b = inst.InstLocalTee(b, 3)
			b = inst.InstI32Const(b, tag)
			b = memory.InstI32Store(b, 2, 0) // tag @ +0
			// payload string @ +8/+12
			b = inst.InstLocalGet(b, 3)
			b = inst.InstI32Const(b, 8)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalGet(b, 1) // path_data
			b = memory.InstI32Store(b, 2, 0)
			b = inst.InstLocalGet(b, 3)
			b = inst.InstI32Const(b, 12)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalGet(b, 2) // path_len
			b = memory.InstI32Store(b, 2, 0)
			b = inst.InstLocalGet(b, 3)
			b = inst.InstReturn(b)
		}
		b = inst.InstEnd(b)
		return b
	}
	emitNullaryCase := func(b []byte, errnoVal, tag int32) []byte {
		// if errno == errnoVal { return Interrupted|Unsupported }
		b = inst.InstLocalGet(b, 0)
		b = inst.InstI32Const(b, errnoVal)
		b = numeric.InstI32Eq(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		{
			b = inst.InstI32Const(b, 4) // 4-byte tag-only variant
			b = inst.InstCall(b, allocBox)
			b = inst.InstLocalTee(b, 3)
			b = inst.InstI32Const(b, tag)
			b = memory.InstI32Store(b, 2, 0)
			b = inst.InstLocalGet(b, 3)
			b = inst.InstReturn(b)
		}
		b = inst.InstEnd(b)
		return b
	}

	body = emitSingleStringCase(body, errnoNoEnt, ioErrTagNotFound)
	body = emitSingleStringCase(body, errnoAccess, ioErrTagPermissionDenied)
	body = emitSingleStringCase(body, errnoExist, ioErrTagAlreadyExists)
	body = emitSingleStringCase(body, errnoIlseq, ioErrTagInvalidUtf8)
	body = emitNullaryCase(body, errnoIntr, ioErrTagInterrupted)
	body = emitNullaryCase(body, errnoNoTsup, ioErrTagUnsupported)

	// Default: Other(path, "io error") — 24 bytes. We don't
	// have a way to materialise a constant inline string here
	// (no string data segment for runtime helpers), so use an
	// empty second string. Callers reading the message will see
	// (empty) which is a downgrade from WAT's "io error"
	// literal; acceptable for the first wasmbin slice — the
	// shaped variant tag still drives match-arm dispatch.
	body = inst.InstI32Const(body, 24)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstI32Const(body, ioErrTagOther)
	body = memory.InstI32Store(body, 2, 0) // tag

	// path string @ +8/+12
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)

	// empty msg @ +16/+20
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // data ptr = 0 (empty)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 20)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // len = 0
	body = memory.InstI32Store(body, 2, 0)

	body = inst.InstLocalGet(body, 3)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadFileBody assembles __fern_read_file.
//
// Signature: (path_data, path_len) → i32 (heap-form
// Result[string, IoError] pointer).
//
// Layout:
//
//	scratch[0..3]:   path_open output (newfd)
//	scratch[4..7]:   iov.base for fd_read
//	scratch[8..11]:  iov.len  for fd_read
//	scratch[12..15]: fd_read output (nread)
//
// Path: alloc 16-byte scratch → normalize the path into a heap
// buffer (SSO-safe; short paths are encoded inline by the
// string runtime) → path_open(fd=3, …, retptr) →
// errno-check → if open failed, wrap errno via
// __build_io_error and return Err. Otherwise loop fd_read with
// a 4 KiB doubling buffer, fd_close, materialise the string,
// wrap as Ok and return.
//
// Locals (after the two params):
//
//	0: $path_data           (param)
//	1: $path_len            (param)
//	2: $scratch             4-word WASI ABI scratch
//	3: $errno               path_open errno
//	4: $fd                  opened fd
//	5: $buf                 growing accumulator (heap ptr)
//	6: $buf_size            current capacity (4096 → doubles)
//	7: $cur                 bytes accumulated so far
//	8: $nread               fd_read return-count
//	9: $new_buf             scratch for buffer doubling
//	10: $new_size           scratch for buffer doubling
//	11: $strbuf             final string data heap ptr
//	12: $result             heap-form Result pointer
//	13: $path_buf           SSO-normalized path data (heap ptr)
//	14: $path_byte_len      decoded byte length of the path
//	15: $i_path             str-normalize loop counter
func buildReadFileBody(idxs map[string]uint32) []byte {
	return buildReadFileBodyCommon(idxs, false)
}

// buildReadFileBytesBody assembles __fern_read_file_bytes —
// read_file's raw sibling. Same pipeline and error
// classification; the contents land in a fresh u8[] from
// __alloc_u8 (16-byte cap/rc/len header behind the data ptr)
// and the Ok box carries the array data pointer.
func buildReadFileBytesBody(idxs map[string]uint32) []byte {
	return buildReadFileBodyCommon(idxs, true)
}

func buildReadFileBodyCommon(idxs map[string]uint32, asBytes bool) []byte {
	// Reused for path_open scratch / str-normalize temps AND the file
	// content string buffer → rc1 so the returned string reclaims
	// (over-headering the temps is harmless carrier-side).
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	pathOpen := idxs["wasi_path_open"]
	fdRead := idxs["wasi_fd_read"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte

	// Scratch FIRST — keeps the path_open retptr 4-byte aligned
	// even after the str-normalize allocations consume a few
	// arbitrary-size bytes off the bump cursor.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// Normalize the path so path_open sees a contiguous host-
	// readable byte buffer (the IR's SSO encoding packs strings
	// ≤ 7 bytes into the (data, len) bit pattern itself; that
	// isn't a valid memory address).
	body = emitStrNormalize(body, idxs, 0, 1, 13, 14, 15)

	// errno = path_open(dirfd=3, dirflags=1, path_buf, path_byte_len,
	//                    oflags=0, fs_rights_base=RIGHT_FD_READ,
	//                    fs_rights_inheriting=RIGHT_FD_READ,
	//                    fdflags=0, retptr=scratch)
	body = inst.InstI32Const(body, preopenDirfd)                    // dirfd
	body = inst.InstI32Const(body, 1)                               // dirflags (symlink_follow)
	body = inst.InstLocalGet(body, 13)                              // path_buf
	body = inst.InstLocalGet(body, 14)                              // path_byte_len
	body = inst.InstI32Const(body, 0)                               // oflags
	body = inst.InstI64Const(body, wasiRightFdRead|wasiRightFdSeek) // fs_rights_base
	body = inst.InstI64Const(body, wasiRightFdRead|wasiRightFdSeek) // fs_rights_inheriting
	body = inst.InstI32Const(body, 0)                               // fdflags
	body = inst.InstLocalGet(body, 2)                               // retptr → scratch[0..3]
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 3) // $errno

	// wrapErrReturn expects the errno on the operand stack: builds
	// the IoError via __build_io_error (stashed in local 9 — $new_buf
	// is dead on the error paths) and returns it wrapped in an Err
	// box. The IR's payloadLayout for `Result[String, IoError]`'s Err
	// variant places the (pointer-shaped) IoError payload at offset 4
	// — no 8-byte alignment padding because the slot itself is 4
	// bytes wide. Total Err allocation is 8 bytes (tag at +0, IoError
	// ptr at +4); Ok stays 16 (string is 8-byte-aligned at +8).
	wrapErrReturn := func(b []byte) []byte {
		b = inst.InstLocalGet(b, 0) // path_data
		b = inst.InstLocalGet(b, 1) // path_len
		b = inst.InstCall(b, buildIoErr)
		b = inst.InstLocalSet(b, 9)
		b = inst.InstI32Const(b, 8)
		b = inst.InstCall(b, allocBox)
		b = inst.InstLocalTee(b, 12) // $result
		b = inst.InstI32Const(b, 1)  // tag = 1 (Err)
		b = memory.InstI32Store(b, 2, 0)
		b = inst.InstLocalGet(b, 12)
		b = inst.InstI32Const(b, 4)
		b = numeric.InstI32Add(b)
		b = inst.InstLocalGet(b, 9) // IoError ptr
		b = memory.InstI32Store(b, 2, 0)
		b = inst.InstLocalGet(b, 12)
		b = inst.InstReturn(b)
		return b
	}

	// if errno != 0 { return build_io_error(errno, path_data, path_len) wrapped in Err }
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 3) // errno
		body = wrapErrReturn(body)
	}
	body = inst.InstEnd(body)

	// fd = mem[scratch]
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)

	// buf = alloc(4096); buf_size = 4096; cur = 0
	body = inst.InstI32Const(body, 4096)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstI32Const(body, 4096)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7)

	// block $end { loop $read_loop { ... br $read_loop } }
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// iov.base = buf + cur (stored at scratch+4)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 5) // buf
		body = inst.InstLocalGet(body, 7) // cur
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)

		// iov.len = buf_size - cur (stored at scratch+8)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 6) // buf_size
		body = inst.InstLocalGet(body, 7) // cur
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Store(body, 2, 0)

		// fd_read(fd, scratch+4, 1, scratch+12)
		body = inst.InstLocalGet(body, 4) // fd
		body = inst.InstLocalGet(body, 2) // scratch
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)   // scratch+4 (iov_ptr)
		body = inst.InstI32Const(body, 1) // iovs_count
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body) // scratch+12 (nread_ptr)
		body = inst.InstCall(body, fdRead)
		body = inst.InstDrop(body) // ignore errno (treat as EOF)

		// nread = mem[scratch+12]
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, 8) // $nread

		// if nread == 0 br $end
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1) // depth 1 = block $end

		// cur += nread
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)

		// If cur < buf_size, loop back without growing.
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32LtS(body)
		body = inst.InstBrIf(body, 0) // depth 0 = $read_loop

		// new_size = buf_size << 1
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Shl(body)
		body = inst.InstLocalTee(body, 10) // $new_size

		// new_buf = alloc(new_size)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalTee(body, 9) // $new_buf

		// memory.copy(new_buf, buf, cur)
		body = inst.InstLocalGet(body, 5) // src = buf
		body = inst.InstLocalGet(body, 7) // n   = cur
		body = memory.InstMemoryCopy(body)

		// buf = new_buf; buf_size = new_size
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalSet(body, 5)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstLocalSet(body, 6)

		body = inst.InstBr(body, 0) // br $read_loop
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// fd_close(fd) → drop errno
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, fdClose)
	body = inst.InstDrop(body)

	if !asBytes {
		// read_file validates UTF-8 (D9, #5714); read_file_bytes is
		// the unvalidated escape hatch. The accumulator holds exactly
		// the bytes read (cur grows to EOF — no fstat-sized shrink
		// tail to zero-fill), so validation sees only file content.
		body = inst.InstLocalGet(body, 5) // buf
		body = inst.InstLocalGet(body, 7) // cur
		body = inst.InstCall(body, idxs["__fern_utf8_valid"])
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, errnoIlseq)
			body = wrapErrReturn(body)
		}
		body = inst.InstEnd(body)
	}

	if asBytes {
		// arr = __alloc_u8(cur); memory.copy(arr, buf, cur).
		// __alloc_u8 owns the cap/rc/len header and the len is
		// baked in, so no explicit length store here.
		body = inst.InstLocalGet(body, 7) // cur
		body = inst.InstCall(body, idxs["__alloc_u8"])
		body = inst.InstLocalTee(body, 11) // $strbuf (array data ptr)
		body = inst.InstLocalGet(body, 5)  // buf
		body = inst.InstLocalGet(body, 7)  // cur
		body = memory.InstMemoryCopy(body)

		// Build Ok(u8[]) — 8 bytes: tag=0 @ 0, array data ptr
		// @ +4 (single-word payload, same slot rule as the Err
		// arm).
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 12) // $result
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0) // tag = 0 (Ok)
		body = inst.InstLocalGet(body, 12)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = memory.InstI32Store(body, 2, 0) // arr ptr @ +4
		body = inst.InstLocalGet(body, 12)
	} else {
		// strbuf = alloc(cur); memory.copy(strbuf, buf, cur)
		body = inst.InstLocalGet(body, 7) // cur
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalTee(body, 11) // $strbuf
		body = inst.InstLocalGet(body, 5)  // buf
		body = inst.InstLocalGet(body, 7)  // cur
		body = memory.InstMemoryCopy(body)

		// Build Ok(string) — 16 bytes: tag=0 @ 0, data @ +8, len @ +12.
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 12) // $result
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0) // tag = 0 (Ok)

		body = inst.InstLocalGet(body, 12)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = memory.InstI32Store(body, 2, 0) // data @ +8

		body = inst.InstLocalGet(body, 12)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 7)      // cur
		body = memory.InstI32Store(body, 2, 0) // len @ +12

		body = inst.InstLocalGet(body, 12)
	}

	// Locals declaration: 14 i32 locals (slots 2..15) — the 11
	// originals plus the path-normalize scratch trio.
	locals := inst.PutLocalsOneGroup(nil, 14, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadFileBodyP2 is the preview-2 variant of buildReadFileBody.
// Reads a whole file through the wasi:filesystem chain instead of
// preview-1 path_open/fd_read:
//
//	base   = get-directories() -> first preopen descriptor handle
//	fd     = base.open-at(symlink-follow, path, 0, read) -> descriptor
//	stream = fd.read-via-stream(0) -> input-stream
//	loop: stream.blocking-read(4096) -> list<u8> until closed (EOF)
//
// Returns Result[string, IoError]: Ok(contents) on success, Err on
// open/read failure. The blocking-read chunks are host-allocated
// (cabi_realloc) and copied into a doubling accumulator.
//
// Known simplification: open/read errors map to ENOENT (NotFound)
// via __build_io_error rather than translating each error-code
// case; blocking-read errors are treated as end-of-stream. Refining
// the error-code → IoError mapping is a follow-up.
//
// Locals (after 2 params): 2=rb, 3=path_buf, 4=path_byte_len,
// 5=preopen, 6=fd, 7=stream, 8=acc_buf, 9=acc_size, 10=acc_cur,
// 11=chunk_ptr, 12=chunk_len, 13=box/tmp, 14=strnorm scratch,
// 15=ioerr.
func buildReadFileBodyP2(idxs map[string]uint32) []byte {
	return buildReadFileBodyP2Common(idxs, false)
}

// buildReadFileBytesBodyP2 is the preview-2 variant of
// buildReadFileBytesBody: buildReadFileBodyP2's pipeline with the
// contents in an __alloc_u8 box and the Ok payload the array
// data pointer.
func buildReadFileBytesBodyP2(idxs map[string]uint32) []byte {
	return buildReadFileBodyP2Common(idxs, true)
}

func buildReadFileBodyP2Common(idxs map[string]uint32, asBytes bool) []byte {
	// Reused for acc/chunk scratch AND the file content string buffer →
	// rc1 for reclamation (over-headering the temps is harmless).
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	readVia := idxs["wasi_descriptor_read_via_stream_p2"]
	blockingRead := idxs["wasi_io_blocking_read"]

	var body []byte
	// rb = alloc(16) — reused retbuf for the sequential host calls.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// Normalize path → path_buf(3), path_byte_len(4).
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 14)

	// get-directories(rb): list header (base @ rb+0, count @ rb+4).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, getDirs)
	// preopen = mem[mem[rb+0] + 0]  (first tuple's descriptor handle)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)

	// open-at(preopen, path-flags=1 symlink-follow, path_buf,
	//   path_byte_len, open-flags=0, descriptor-flags=1 read, rb)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, openAt)
	// if mem8[rb+0] != 0 → Err(NotFound)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// fd = mem[rb+4]
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 6)

	// read-via-stream(fd, offset=0, rb)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI64Const(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, readVia)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// stream = mem[rb+4]
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 7)

	// acc_buf = alloc(4096); acc_size = 4096; acc_cur = 0
	body = inst.InstI32Const(body, 4096)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstI32Const(body, 4096)
	body = inst.InstLocalSet(body, 9)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 10)

	// block $end { loop $read { ... } }
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// blocking-read(stream, 4096, rb)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI64Const(body, 4096)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, blockingRead)
		// disc != 0 → end of stream (closed / EOF) → break.
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstBrIf(body, 1) // depth 1 = $end
		// chunk_ptr = mem[rb+4]; chunk_len = mem[rb+8]
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstLocalSet(body, 11)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Load(body, 2, 8)
		body = inst.InstLocalTee(body, 12)
		// if chunk_len == 0 → break
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		// Grow accumulator while acc_cur + chunk_len > acc_size.
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// if acc_cur + chunk_len <= acc_size: break grow loop
			body = inst.InstLocalGet(body, 10)
			body = inst.InstLocalGet(body, 12)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 9)
			body = numeric.InstI32LeU(body)
			body = inst.InstBrIf(body, 1)
			// acc_size <<= 1; new = alloc(acc_size); copy(new, acc_buf, acc_cur)
			body = inst.InstLocalGet(body, 9)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			body = inst.InstLocalSet(body, 9)
			body = inst.InstLocalGet(body, 9)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalSet(body, 13)
			// memory.copy(new=13, acc_buf=8, acc_cur=10); then acc_buf = new.
			body = inst.InstLocalGet(body, 13)
			body = inst.InstLocalGet(body, 8)
			body = inst.InstLocalGet(body, 10)
			body = memory.InstMemoryCopy(body)
			body = inst.InstLocalGet(body, 13)
			body = inst.InstLocalSet(body, 8)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)
		// memory.copy(acc_buf + acc_cur, chunk_ptr, chunk_len)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 10)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 12)
		body = memory.InstMemoryCopy(body)
		// acc_cur += chunk_len
		body = inst.InstLocalGet(body, 10)
		body = inst.InstLocalGet(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 10)
		body = inst.InstBr(body, 0) // $read
	}
	// end $read loop, end $end block
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	if !asBytes {
		// read_file validates UTF-8 (D9, #5714); read_file_bytes is
		// the unvalidated escape hatch. acc_cur is exactly the bytes
		// streamed to EOF — no stat-sized shrink tail to zero-fill.
		// The synthetic EILSEQ goes straight to __build_io_error,
		// bypassing the p2 error-code translator (it maps
		// wasi:filesystem error-codes, not errnos).
		body = inst.InstLocalGet(body, 8)  // acc_buf
		body = inst.InstLocalGet(body, 10) // acc_cur
		body = inst.InstCall(body, idxs["__fern_utf8_valid"])
		body = numeric.InstI32Eqz(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, errnoIlseq)
			body = inst.InstLocalSet(body, 16)
			body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
		}
		body = inst.InstEnd(body)
	}

	if asBytes {
		// arr = __alloc_u8(acc_cur); memory.copy(arr, acc_buf, acc_cur)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstCall(body, idxs["__alloc_u8"])
		body = inst.InstLocalTee(body, 11) // reuse $chunk_ptr as arr data ptr
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 10)
		body = memory.InstMemoryCopy(body)
		// Build Ok(u8[]): box(8) tag=0 @0, array data ptr @+4.
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 13)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 13)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 13)
	} else {
		// strbuf = alloc(acc_cur); memory.copy(strbuf, acc_buf, acc_cur)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalTee(body, 11) // reuse $chunk_ptr as $strbuf
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 10)
		body = memory.InstMemoryCopy(body)
		// Build Ok(string): box(16) tag=0 @0, data @+8, len @+12.
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 13)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 13)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 13)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 10)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 13)
	}

	// 15 i32 locals (2..16): the 14 originals plus local 16 (errno
	// from the mapped error-code).
	locals := inst.PutLocalsOneGroup(nil, 15, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadFileErr appends the "build IoError(errno) and return
// Err" tail used by buildReadFileBodyP2's open/read error paths.
// Uses local 15 for the IoError ptr and local 13 for the Result
// box. Mirrors buildReadFileBody's Err shape (8-byte box, tag=1 @0,
// IoError ptr @+4).
// appendErrnoFromErrorCode maps the wasi:filesystem error-code
// discriminant at mem8[rbLocal+4] (the err arm of
// result<_, error-code>) to the preview-1 errno __build_io_error
// understands, writing it into errnoLocal. Default ENOENT
// (NotFound); the recognised cases line up with __build_io_error's
// errno→IoError-variant map (access → PermissionDenied, exist →
// AlreadyExists, interrupted → Interrupted, unsupported →
// Unsupported). error-code disc indices follow
// WasiFilesystemErrorCodeNames order.
func appendErrnoFromErrorCode(body []byte, rbLocal, errnoLocal uint32) []byte {
	return appendErrnoFromErrorCodeAt(body, rbLocal, errnoLocal, 4)
}

// appendErrnoFromErrorCodeAt is appendErrnoFromErrorCode with the
// error-code's offset in the return area spelled out. It is 4 for every
// result whose ok arm is a single word or empty, but the arm's
// alignment sets it: `result<descriptor-stat, error-code>` puts the
// error-code at 8, because descriptor-stat's u64 fields make the whole
// payload 8-aligned. Reading it at 4 there yields the top half of a
// zeroed word — a silent "ENOENT for everything".
func appendErrnoFromErrorCodeAt(body []byte, rbLocal, errnoLocal uint32, codeOff uint32) []byte {
	// errno = ENOENT (default / no-entry).
	body = inst.InstI32Const(body, errnoNoEnt)
	body = inst.InstLocalSet(body, errnoLocal)
	for _, m := range []struct{ discVal, errnoVal int32 }{
		{0, errnoAccess}, {7, errnoExist}, {11, errnoIntr}, {27, errnoNoTsup},
	} {
		// if mem8[rb+codeOff] == discVal { errno = errnoVal }
		body = inst.InstLocalGet(body, rbLocal)
		body = memory.InstI32Load8U(body, 0, codeOff)
		body = inst.InstI32Const(body, m.discVal)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, m.errnoVal)
		body = inst.InstLocalSet(body, errnoLocal)
		body = inst.InstEnd(body)
	}
	return body
}

func buildReadFileErr(body []byte, idxs map[string]uint32, buildIoErr, allocBox, errnoLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	// __build_io_error(errno, path_data, path_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, 15)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 13)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 13)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 15)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 13)
	body = inst.InstReturn(body)
	return body
}

// emitStrNormalize copies an SSO-encoded `(data, len)` string
// into a fresh heap buffer of its byte length, returning the
// heap buffer pointer + byte length as two consecutive op
// sequences. Caller supplies the two source locals (data, len)
// + the per-call scratch locals (bufLocal, lenLocal, iLocal).
// Leaves nothing on the operand stack on entry/exit; bufLocal
// holds the heap pointer and lenLocal holds the byte length.
//
// Mirrors the SSO-aware copy loop in buildPrintBodyFd —
// path_open's path argument must be a contiguous byte buffer
// in linear memory, so inline-form strings (high bit on len)
// can't be passed straight through.
func emitStrNormalize(body []byte, idxs map[string]uint32, dataLocal, lenLocal, bufLocal, byteLenLocal, iLocal uint32) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	alloc := idxs["__fern_alloc"]

	// byteLen = __fern_str_len(data, len)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstLocalGet(body, lenLocal)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalTee(body, byteLenLocal)

	// buf = alloc(byteLen)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, bufLocal)

	// for i in 0..byteLen: mem[buf+i] = __fern_str_byte(data, len, i)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, iLocal)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, iLocal)
		body = inst.InstLocalGet(body, byteLenLocal)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1) // exit on i >= byteLen

		body = inst.InstLocalGet(body, bufLocal)
		body = inst.InstLocalGet(body, iLocal)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, dataLocal)
		body = inst.InstLocalGet(body, lenLocal)
		body = inst.InstLocalGet(body, iLocal)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)

		body = inst.InstLocalGet(body, iLocal)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, iLocal)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	return body
}

// buildWriteFileBody assembles __fern_write_file.
//
// Signature: (path_data, path_len, content_data, content_len) →
// i32 (heap-form Option[IoError] pointer; None on success,
// Some(IoError) on error).
//
// Pipeline:
//
//  1. Normalize path and content into heap buffers (SSO-safe).
//  2. path_open with O_CREAT|O_TRUNC + write rights, fd=3
//     preopen.
//  3. On errno != 0: build IoError, wrap in Some(IoError),
//     return.
//  4. Loop fd_write until all content bytes drained.
//  5. fd_close.
//  6. Return None (4-byte alloc, tag = 1).
//
// Locals (after the four params 0..3):
//
//	4:  $scratch        (16-byte WASI ABI scratch)
//	5:  $errno          (path_open / fd_write errno)
//	6:  $fd             (opened file descriptor)
//	7:  $path_buf       (heap-normalized path data)
//	8:  $path_byte_len  (heap-normalized path length)
//	9:  $i_path         (scratch loop counter for path normalize)
//	10: $content_buf    (heap-normalized content data)
//	11: $content_byte_len
//	12: $i_content
//	13: $cur            (bytes written so far)
//	14: $nwritten       (bytes written this call)
//	15: $err_ptr        (IoError pointer for Some wrapping)
//	16: $result         (heap-form Option pointer)
func buildWriteFileBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	pathOpen := idxs["wasi_path_open"]
	fdWrite := idxs["wasi_fd_write"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte

	// Alloc the WASI ABI scratch FIRST so it lands on a 4-byte
	// boundary. path_open writes the new fd as a u32 to the
	// retptr we pass; wasmtime's host enforces 4-byte alignment
	// on that write, and the bump cursor is only word-aligned at
	// program entry. Doing the string-normalize allocations after
	// (each of which advances the cursor by an arbitrary byte
	// count) keeps the scratch retptr aligned even when the
	// path / content bytes leave the cursor straddling a word.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)

	// Normalize path: locals 0/1 → bufLocal=7, byteLenLocal=8, iLocal=9.
	body = emitStrNormalize(body, idxs, 0, 1, 7, 8, 9)
	// Normalize content: locals 2/3 → bufLocal=10, byteLenLocal=11, iLocal=12.
	body = emitStrNormalize(body, idxs, 2, 3, 10, 11, 12)

	// errno = path_open(dirfd=3, dirflags=1, path_buf, path_byte_len,
	//                    oflags=CREATE|TRUNCATE, fs_rights_base=WRITE+seek,
	//                    fs_rights_inheriting=WRITE+seek, fdflags=0,
	//                    retptr=scratch)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 1) // dirflags
	body = inst.InstLocalGet(body, 7) // path_buf
	body = inst.InstLocalGet(body, 8) // path_byte_len
	body = inst.InstI32Const(body, wasiOflagCreate|wasiOflagTruncate)
	body = inst.InstI64Const(body, wasiRightFdWrite|wasiRightFdSeek)
	body = inst.InstI64Const(body, wasiRightFdWrite|wasiRightFdSeek)
	body = inst.InstI32Const(body, 0) // fdflags
	body = inst.InstLocalGet(body, 4) // retptr → scratch[0..3]
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 5) // $errno

	// if errno != 0 { build IoError → Some → return }
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5) // errno
		body = inst.InstLocalGet(body, 0) // path_data (original — keeps SSO bits)
		body = inst.InstLocalGet(body, 1) // path_len
		body = inst.InstCall(body, buildIoErr)
		body = inst.InstLocalSet(body, 15) // $err_ptr

		// Some(IoError) layout (Option pair-form NOT used here —
		// runtime helpers return heap-form via OpCallDirect):
		// 8 bytes, tag=0 @ +0, IoError ptr @ +4.
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 16) // $result
		body = inst.InstI32Const(body, 1)  // tag = 1 (Err)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 16)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 15)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 16)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// fd = mem[scratch]
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 6)

	// Write loop. iov at scratch+4..+11; nwritten at scratch+12.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 13) // $cur = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if cur >= content_byte_len, break
		body = inst.InstLocalGet(body, 13)
		body = inst.InstLocalGet(body, 11)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)

		// iov.base = content_buf + cur
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstLocalGet(body, 13)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)

		// iov.len = content_byte_len - cur
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 13)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Store(body, 2, 0)

		// fd_write(fd, scratch+4, 1, scratch+12)
		body = inst.InstLocalGet(body, 6) // fd
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, 1)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, fdWrite)
		body = inst.InstDrop(body) // ignore errno mid-stream (matches WAT)

		// nwritten = mem[scratch+12]
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, 14)

		// if nwritten == 0, break (avoid infinite loop on short writes).
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)

		// cur += nwritten
		body = inst.InstLocalGet(body, 13)
		body = inst.InstLocalGet(body, 14)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 13)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// fd_close(fd) → drop errno
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, fdClose)
	body = inst.InstDrop(body)

	// Return Ok(()): 8-byte alloc, tag = 0 @ +0, unit payload @ +4.
	// The unit occupies a payload slot like any other value — not the
	// 4-byte tag-only box Option uses.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 16)
	body = inst.InstI32Const(body, 0) // tag = 0 (Ok)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 16)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // unit payload
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 16)

	// 13 i32 locals after the 4 params (slots 4..16).
	locals := inst.PutLocalsOneGroup(nil, 13, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriteFileBodyP2 is the preview-2 variant of buildWriteFileBody.
// Writes a file through the wasi:filesystem chain:
//
//	base   = get-directories() -> first preopen descriptor
//	fd     = base.open-at(symlink-follow, path, create|truncate, write)
//	stream = fd.write-via-stream(0) -> output-stream
//	loop: stream.blocking-write-and-flush(chunk ≤ 4096) until done
//
// Returns Option[IoError]: None on success, Some(err) on
// open/write failure. Same ENOENT simplification as
// buildReadFileBodyP2.
//
// Locals (after 4 params: path 0/1, content 2/3): 4=rb, 5=path_buf,
// 6=path_byte_len, 7=content_buf, 8=content_byte_len, 9=preopen,
// 10=fd, 11=stream, 12=cur, 13=chunk_len, 14=strnorm scratch,
// 15=box, 16=ioerr.
func buildWriteFileBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	writeVia := idxs["wasi_descriptor_write_via_stream_p2"]
	blockingWrite := idxs["wasi_blocking_write_and_flush_p2"]

	// open-flags: create(bit0) | truncate(bit3) = 1 | 8 = 9.
	const openFlagsCreateTrunc = 9
	// descriptor-flags: write = bit1 = 2.
	const descFlagsWrite = 2

	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)
	body = emitStrNormalize(body, idxs, 0, 1, 5, 6, 14)
	body = emitStrNormalize(body, idxs, 2, 3, 7, 8, 14)

	// get-directories(rb); preopen = mem[mem[rb+0]+0]
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, getDirs)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 9)

	// open-at(preopen, 1, path_buf, path_byte_len, create|truncate, write, rb)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, openFlagsCreateTrunc)
	body = inst.InstI32Const(body, descFlagsWrite)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, openAt)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 4, 17)
		body = buildWriteFileErr(body, buildIoErr, allocBox, 17)
	}
	body = inst.InstEnd(body)
	// fd = mem[rb+4]
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 10)

	// write-via-stream(fd, offset=0, rb)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI64Const(body, 0)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstCall(body, writeVia)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 4, 17)
		body = buildWriteFileErr(body, buildIoErr, allocBox, 17)
	}
	body = inst.InstEnd(body)
	// stream = mem[rb+4]
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 11)

	// cur = 0; loop: write chunks of ≤ 4096.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 12)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if cur >= content_byte_len: break
		body = inst.InstLocalGet(body, 12)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// chunk_len = min(content_byte_len - cur, 4096)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 12)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 13)
		// if chunk_len > 4096: chunk_len = 4096
		body = inst.InstLocalGet(body, 13)
		body = inst.InstI32Const(body, 4096)
		body = numeric.InstI32GtU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 4096)
			body = inst.InstLocalSet(body, 13)
		}
		body = inst.InstEnd(body)
		// blocking-write-and-flush(stream, content_buf+cur, chunk_len, rb)
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 13)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, blockingWrite)
		// disc != 0 → Some(IoError). This err arm carries a
		// stream-error (not an error-code), so don't run the
		// error-code mapper; use a generic errno (0 → __build_io_error
		// default variant).
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalSet(body, 17)
			body = buildWriteFileErr(body, buildIoErr, allocBox, 17)
		}
		body = inst.InstEnd(body)
		// cur += chunk_len
		body = inst.InstLocalGet(body, 12)
		body = inst.InstLocalGet(body, 13)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 12)
		body = inst.InstBr(body, 0)
	}
	// end $write loop, end $end block
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	// Success → Ok(()): box(8), tag=0 @ +0, unit payload @ +4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 15)
	body = inst.InstI32Const(body, 0) // tag = 0 (Ok)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 15)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 0) // unit payload
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 15)

	// 14 i32 locals (4..17): the 13 originals plus local 17 (errno
	// from the mapped error-code).
	locals := inst.PutLocalsOneGroup(nil, 14, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriteFileErr appends the "build Some(IoError(errno)) and
// return" tail for buildWriteFileBodyP2's failure paths. Uses local
// 16 for the IoError ptr, local 15 for the Option box (Some: tag=0
// @0, IoError ptr @+4 — the heap-form Option[IoError] layout).
func buildWriteFileErr(body []byte, buildIoErr, allocBox, errnoLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	// __build_io_error(errno, path_data, path_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, buildIoErr)
	body = inst.InstLocalSet(body, 16)
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 15)
	body = inst.InstI32Const(body, 1) // tag = 1 (Err)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 15)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 16)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 15)
	body = inst.InstReturn(body)
	return body
}

// buildOpenBody is the shared body builder for open_reader /
// open_writer / open_appender. They differ only in the
// path_open immediate flags + the rights bitset; the rest of
// the pipeline (path normalize → path_open → Result wrap) is
// identical.
//
// On success constructs a 4-byte Reader/Writer struct
// (`{ fd: i32 }`) on the heap and wraps it in `Ok(struct)`.
// On `errno != 0` builds the same `__build_io_error` variant
// and wraps in `Err(IoError)`. Both arms produce the
// pointer-shaped 8-byte Result layout the IR's `payloadLayout`
// expects for `Result[Ptr, Ptr]` (tag@0, payload-ptr@4).
//
// Locals (after the two params):
//
//	0: $path_data         (param)
//	1: $path_len          (param)
//	2: $scratch           4-word WASI ABI scratch
//	3: $errno             path_open errno
//	4: $fd                opened fd
//	5: $reader_or_writer  heap-allocated 4-byte struct
//	6: $err_ptr           IoError pointer for Err wrapping
//	7: $result            heap-form Result pointer
//	8: $path_buf          SSO-normalized path data (heap ptr)
//	9: $path_byte_len     decoded byte length of the path
//	10: $i_path           str-normalize loop counter
func buildOpenBody(idxs map[string]uint32, oflags int32, rights int64, fdflags int32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	pathOpen := idxs["wasi_path_open"]

	var body []byte

	// scratch FIRST (16 bytes — 4-aligned because alloc rounds).
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// Normalize path into a heap buffer.
	body = emitStrNormalize(body, idxs, 0, 1, 8, 9, 10)

	// errno = path_open(dirfd=3, dirflags=1, path_buf, path_byte_len,
	//                    oflags, rights, rights, fdflags, retptr=scratch)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 1) // dirflags (symlink_follow)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, oflags)
	body = inst.InstI64Const(body, rights)
	body = inst.InstI64Const(body, rights)
	body = inst.InstI32Const(body, fdflags)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 3) // $errno

	// if errno != 0 { build IoError → Err → return }
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 3) // errno
		body = inst.InstLocalGet(body, 0) // path_data (original)
		body = inst.InstLocalGet(body, 1) // path_len
		body = inst.InstCall(body, buildIoErr)
		body = inst.InstLocalSet(body, 6) // $err_ptr

		// Result.Err layout: 8 bytes, tag=1 @ +0, IoError ptr @ +4.
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 7)
		body = inst.InstI32Const(body, 1) // tag = 1 (Err)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 6)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// fd = mem[scratch]
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)

	// Build Reader/Writer struct: 12 bytes total — 8-byte rc
	// header (static-sentinel 0x80000000 at base+0 so
	// __fern_rc_inc/dec short-circuit per Phase 1e-runtime) +
	// 4-byte `{fd: i32}` payload at base+8. The slot stores
	// the user-visible data pointer = base + 8.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 5)
	// Static sentinel at base + 0.
	body = inst.InstI32Const(body, -0x80000000) // 0x80000000 as i32
	body = memory.InstI32Store(body, 2, 0)
	// Shift slot 5 from base to base + 8 (= data pointer).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 5)
	// fd at data + 0 (= base + 8).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 4) // fd
	body = memory.InstI32Store(body, 2, 0)

	// Result.Ok layout: 8 bytes, tag=0 @ +0, struct ptr @ +4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 7)
	body = inst.InstI32Const(body, 0) // tag = 0 (Ok)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)

	// 9 i32 locals after the 2 params (slots 2..10).
	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildOpenReaderBody — open with read-only rights and no
// oflags. Returns Result[Reader, IoError].
func buildOpenReaderBody(idxs map[string]uint32) []byte {
	return buildOpenBody(idxs, 0, wasiRightFdRead|wasiRightFdSeek, 0)
}

// buildOpenReaderBodyP2 is the preview-2 variant of open_reader.
// Preview-2 has no fds, so the Reader holds an input-stream handle:
// the get-directories → open-at → read-via-stream chain (identical
// to buildReadFileBodyP2's open prefix) yields a stream handle that
// is stored in the same 12-byte Reader struct buildOpenBody builds
// (8-byte rc sentinel + {handle: i32} at +8). Returns
// Result[Reader, IoError]; the two host-call error checks map the
// error-code to an IoError via buildReadFileErr. The Reader's
// read_line / read_chunk methods then blocking-read on the handle.
//
// Locals mirror buildReadFileBodyP2 (15 locals, slots 2..16) so the
// shared buildReadFileErr / appendErrnoFromErrorCode helpers line
// up: 2=rb, 3=path_buf, 4=path_byte_len, 5=preopen, 6=fd, 7=stream,
// 8=reader base/data, 9=Ok box, 13/15/16 used by the err helper,
// 14=normalize scratch.
func buildOpenReaderBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	readVia := idxs["wasi_descriptor_read_via_stream_p2"]

	var body []byte
	// rb = alloc(16) — shared retbuf for the host calls.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	// Normalize path → path_buf(3), path_byte_len(4).
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 14)
	// get-directories(rb); preopen = mem[mem[rb+0]].
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, getDirs)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	// open-at(preopen, path-flags=1, path_buf, path_byte_len,
	//   open-flags=0, descriptor-flags=1 read, rb).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, openAt)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// fd = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 6)
	// read-via-stream(fd, offset=0, rb).
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI64Const(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, readVia)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// stream = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 7)

	// Reader struct: 12 bytes (rc sentinel @ +0, {handle} @ +8).
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 8)
	body = inst.InstI32Const(body, -0x80000000) // static rc sentinel
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 8) // data pointer = base + 8
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 7) // stream handle
	body = memory.InstI32Store(body, 2, 0)

	// Result.Ok: 8 bytes, tag=0 @ +0, Reader ptr @ +4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)

	locals := inst.PutLocalsOneGroup(nil, 15, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildOpenWriterBody — open with CREATE|TRUNCATE + write
// rights. Returns Result[Writer, IoError].
func buildOpenWriterBody(idxs map[string]uint32) []byte {
	return buildOpenBody(idxs,
		wasiOflagCreate|wasiOflagTruncate,
		wasiRightFdWrite|wasiRightFdSeek,
		0)
}

// buildOpenWriterBodyP2 is the preview-2 variant of open_writer:
// the write-side mirror of buildOpenReaderBodyP2. Opens via
// get-directories → open-at (create|truncate, write) →
// write-via-stream and stores the output-stream handle in the same
// 12-byte Writer struct (rc sentinel + {handle} at +8). Returns
// Result[Writer, IoError]; the host-call error checks build a
// Result.Err via buildReadFileErr (tag=1). The Writer's write
// method blocking-write-and-flushes on the handle.
//
// Locals match buildOpenReaderBodyP2 (15, slots 2..16): 2=rb,
// 3=path_buf, 4=path_byte_len, 5=preopen, 6=fd, 7=stream,
// 8=writer base/data, 9=Ok box, 13/15/16 used by the err helper,
// 14=normalize scratch.
func buildOpenWriterBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	writeVia := idxs["wasi_descriptor_write_via_stream_p2"]

	// open-flags: create(bit0)|truncate(bit3) = 9; descriptor-flags: write = 2.
	const openFlagsCreateTrunc = 9
	const descFlagsWrite = 2

	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 14)
	// get-directories(rb); preopen = mem[mem[rb+0]].
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, getDirs)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	// open-at(preopen, 1, path_buf, path_byte_len, create|trunc, write, rb).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, openFlagsCreateTrunc)
	body = inst.InstI32Const(body, descFlagsWrite)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, openAt)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// fd = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 6)
	// write-via-stream(fd, offset=0, rb).
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI64Const(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, writeVia)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// stream = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 7)

	// Writer struct: 12 bytes (rc sentinel @ +0, {handle} @ +8).
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 8)
	body = inst.InstI32Const(body, -0x80000000)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 8) // data pointer = base + 8
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Store(body, 2, 0)

	// Result.Ok: 8 bytes, tag=0 @ +0, Writer ptr @ +4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)

	locals := inst.PutLocalsOneGroup(nil, 15, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildOpenAppenderBodyP2 is the preview-2 variant of open_appender:
// like buildOpenWriterBodyP2 but opens with CREATE only (no TRUNCATE,
// so an existing file's contents are kept) and uses append-via-stream
// — which takes no offset and returns an output-stream already
// positioned at end-of-file, so Writer.write appends. Returns
// Result[Writer, IoError]. Locals mirror buildOpenWriterBodyP2.
func buildOpenAppenderBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	getDirs := idxs["wasi_get_directories_p2"]
	openAt := idxs["wasi_descriptor_open_at_p2"]
	appendVia := idxs["wasi_descriptor_append_via_stream_p2"]

	// open-flags: create(bit0) only = 1; descriptor-flags: write = 2.
	const openFlagsCreate = 1
	const descFlagsWrite = 2

	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	body = emitStrNormalize(body, idxs, 0, 1, 3, 4, 14)
	// get-directories(rb); preopen = mem[mem[rb+0]].
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, getDirs)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	// open-at(preopen, 1, path_buf, path_byte_len, create, write, rb).
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, openFlagsCreate)
	body = inst.InstI32Const(body, descFlagsWrite)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, openAt)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// fd = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 6)
	// append-via-stream(fd, rb) — no offset.
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, appendVia)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = appendErrnoFromErrorCode(body, 2, 16)
		body = buildReadFileErr(body, idxs, buildIoErr, allocBox, 16)
	}
	body = inst.InstEnd(body)
	// stream = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 7)

	// Writer struct: 12 bytes (rc sentinel @ +0, {handle} @ +8).
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 8)
	body = inst.InstI32Const(body, -0x80000000)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 7)
	body = memory.InstI32Store(body, 2, 0)

	// Result.Ok: 8 bytes, tag=0 @ +0, Writer ptr @ +4.
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 8)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)

	locals := inst.PutLocalsOneGroup(nil, 15, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildOpenAppenderBody — open with CREATE + write rights +
// fdflag APPEND. Returns Result[Writer, IoError].
func buildOpenAppenderBody(idxs map[string]uint32) []byte {
	return buildOpenBody(idxs,
		wasiOflagCreate,
		wasiRightFdWrite|wasiRightFdSeek,
		wasiFdflagAppend)
}

// buildReaderCloseBody — `__method_Reader_close(r)`. Calls
// fd_close on the Reader's fd; returns None on errno=0,
// Some(IoError) otherwise.
//
// Signature: (r: i32) → i32 (heap-form Option[IoError]).
//
// Locals (after the param):
//
//	0: $r (param)
//	1: $errno
//	2: $err_ptr
//	3: $result
func buildReaderCloseFdBody(idxs map[string]uint32) []byte {
	return buildCloseBody(idxs)
}

// buildWriterCloseBody is identical to buildReaderCloseBody —
// Reader and Writer share the same 4-byte `{ fd: i32 }` shape,
// so the close paths are byte-for-byte equal. Two named entry
// points keep the IR-side name mangling clean
// (`__method_Reader_close` vs `__method_Writer_close`) and
// the funcIdx table can still resolve both.
func buildWriterCloseBody(idxs map[string]uint32) []byte {
	return buildCloseBody(idxs)
}

// buildCloseBody is the shared close pipeline. Inlined into
// both Reader.close and Writer.close — the struct layout is
// identical.
func buildCloseBody(idxs map[string]uint32) []byte {
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte

	// errno = fd_close(mem[$r])
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstCall(body, fdClose)
	body = inst.InstLocalTee(body, 1)

	// if errno != 0 { build IoError → Some → return }
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 1) // errno
		body = inst.InstI32Const(body, 0) // path_data = 0 (empty)
		body = inst.InstI32Const(body, 0) // path_len  = 0
		body = inst.InstCall(body, buildIoErr)
		body = inst.InstLocalSet(body, 2)

		// Some(IoError): 8 bytes, tag=0 @ +0, IoError ptr @ +4.
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 3)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// Return None (4-byte alloc, tag=1).
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)

	// 3 i32 locals after the 1 param.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderCloseFdBodyP2 / buildWriterCloseBodyP2 are the preview-2
// close paths. The Reader / Writer holds an own<input-stream> /
// own<output-stream> handle at offset 0 (not a preview-1 fd), so close
// is a canon resource.drop on that handle rather than fd_close. drop
// returns nothing, so these always return None — selected via
// preview2HelperBodyOverrides.
func buildReaderCloseFdBodyP2(idxs map[string]uint32) []byte {
	return buildStreamCloseBodyP2(idxs, idxs["wasi_io_input_stream_drop"])
}

func buildWriterCloseBodyP2(idxs map[string]uint32) []byte {
	return buildStreamCloseBodyP2(idxs, idxs["wasi_io_output_stream_drop"])
}

// buildStreamCloseBodyP2 drops the stream handle stored at the Reader /
// Writer's offset 0 and returns None ((4-byte alloc, tag=1) — the
// Option[IoError] success form). `drop` is the canon resource.drop
// import for the relevant stream resource.
func buildStreamCloseBodyP2(idxs map[string]uint32, drop uint32) []byte {
	allocBox := idxs["__fern_alloc_box"]

	var body []byte
	// resource.drop(mem[$self+0]) — the own<…stream> handle.
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstCall(body, drop)

	// Return None (4-byte alloc, tag=1).
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 1)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 1)

	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriterWriteBody — `__method_Writer_write(w, s_data, s_len)`.
// Writes the SSO-normalized string bytes to w.fd via fd_write
// in a loop. Returns None on success, Some(IoError) on
// fd_write failure.
//
// Signature: (w, s_data, s_len) → i32 (heap-form Option[IoError]).
//
// Locals (after the three params):
//
//	0: $w        (param)
//	1: $s_data   (param)
//	2: $s_len    (param)
//	3: $scratch
//	4: $fd
//	5: $errno
//	6: $err_ptr
//	7: $result
//	8: $buf      (heap-normalized content)
//	9: $byte_len
//	10: $i_norm
//	11: $cur
//	12: $nwritten
func buildWriterWriteBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	fdWrite := idxs["wasi_fd_write"]

	var body []byte

	// scratch (12 bytes: iov.base, iov.len, nwritten retptr).
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)

	// fd = mem[$w]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)

	// Normalize content into a heap buffer.
	body = emitStrNormalize(body, idxs, 1, 2, 8, 9, 10)

	// Write loop. cur = 0.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if cur >= byte_len, break
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 9)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)

		// iov.base = buf + cur
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 11)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)

		// iov.len = byte_len - cur
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 11)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Store(body, 2, 0)

		// fd_write(fd, scratch, 1, scratch+8)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 1)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, fdWrite)
		body = inst.InstLocalTee(body, 5) // errno

		// On errno != 0 → build IoError → Some → return
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 5)
			body = inst.InstI32Const(body, 0)
			body = inst.InstI32Const(body, 0)
			body = inst.InstCall(body, buildIoErr)
			body = inst.InstLocalSet(body, 6)
			body = inst.InstI32Const(body, 8)
			body = inst.InstCall(body, allocBox)
			body = inst.InstLocalTee(body, 7)
			body = inst.InstI32Const(body, 0)
			body = memory.InstI32Store(body, 2, 0)
			body = inst.InstLocalGet(body, 7)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 6)
			body = memory.InstI32Store(body, 2, 0)
			body = inst.InstLocalGet(body, 7)
			body = inst.InstReturn(body)
		}
		body = inst.InstEnd(body)

		// nwritten = mem[scratch+8]
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, 12)

		// if nwritten == 0, break
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)

		// cur += nwritten
		body = inst.InstLocalGet(body, 11)
		body = inst.InstLocalGet(body, 12)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 11)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// Return None.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 7)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 7)

	// 10 i32 locals after the 3 params (slots 3..12).
	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildWriterWriteBodyP2 is the preview-2 variant of
// __method_Writer_write. The Writer holds an output-stream handle
// (stored by open_writer's write-via-stream); the content is
// written via wasi:io/streams::blocking-write-and-flush in ≤4096
// chunks rather than fd_write. Returns Option[IoError] — None on
// success, Some(IoError) when a chunk's result discriminant is
// non-zero (a stream-error, mapped through __build_io_error with a
// generic errno like buildWriteFileBodyP2's write loop).
//
// Locals (after 3 params w, content_data, content_len): 3=rb,
// 4=handle, 5=content_buf, 6=content_byte_len, 7=cur, 8=chunk_len,
// 9=errptr, 10=box, 14=normalize scratch.
func buildWriterWriteBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	blockingWrite := idxs["wasi_blocking_write_and_flush_p2"]

	var body []byte
	// rb = alloc(16).
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	// handle = mem[w].
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)
	// Normalize content → content_buf(5), content_byte_len(6).
	body = emitStrNormalize(body, idxs, 1, 2, 5, 6, 14)

	// cur = 0; chunked write loop.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if cur >= content_byte_len: break
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// chunk_len = min(content_byte_len - cur, 4096)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 8)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstI32Const(body, 4096)
		body = numeric.InstI32GtU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 4096)
			body = inst.InstLocalSet(body, 8)
		}
		body = inst.InstEnd(body)
		// blocking-write-and-flush(handle, content_buf+cur, chunk_len, rb)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, blockingWrite)
		// disc != 0 → Some(IoError) (stream-error → generic errno 0).
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 0)
			body = inst.InstI32Const(body, 0)
			body = inst.InstI32Const(body, 0)
			body = inst.InstCall(body, buildIoErr)
			body = inst.InstLocalSet(body, 9)
			body = inst.InstI32Const(body, 8)
			body = inst.InstCall(body, allocBox)
			body = inst.InstLocalTee(body, 10)
			body = inst.InstI32Const(body, 0) // tag = 0 (Some)
			body = memory.InstI32Store(body, 2, 0)
			body = inst.InstLocalGet(body, 10)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 9)
			body = memory.InstI32Store(body, 2, 0)
			body = inst.InstLocalGet(body, 10)
			body = inst.InstReturn(body)
		}
		body = inst.InstEnd(body)
		// cur += chunk_len
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	// end write loop, end outer block
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	// Success → None (box(4), tag=1).
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 10)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)

	// 12 i32 locals after the 3 params (slots 3..14).
	locals := inst.PutLocalsOneGroup(nil, 12, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadLineBody — `__method_Reader_read_line(r)`.
// Reads bytes from r.fd one at a time until a newline or EOF.
// Returns Some(line) including the trailing newline if hit,
// or None on EOF before any byte was read. Stream errors
// mid-line are treated as EOF, matching the WAT path.
//
// Signature: (r: i32) → i32 (heap-form Option[string]).
//
// Locals (after the param):
//
//	0: $r        (param)
//	1: $scratch  (16 bytes: iov + nread + 1-byte buf at +12)
//	2: $fd
//	3: $buf      growing accumulator
//	4: $buf_size
//	5: $cur
//	6: $byte     last byte read
//	7: $new_buf
//	8: $new_size
//	9: $strbuf
//	10: $result
func buildReaderReadLineFdBody(idxs map[string]uint32) []byte {
	// Reused for the iov scratch AND the line accumulation buffer that
	// becomes the returned Some(line) string data → rc1 so the string
	// reclaims (scratch over-headering is harmless carrier-side).
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	fdRead := idxs["wasi_fd_read"]

	var body []byte

	// scratch (16 bytes for iov + nread + 1-byte buffer slot)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)

	// fd = mem[$r]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)

	// iov.base = scratch + 12 (the 1-byte buffer slot)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	// iov.len = 1
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)

	// Initial accumulator: 64 bytes; doubles on overflow.
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstI32Const(body, 64)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// fd_read(fd, scratch, 1, scratch+8)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 1)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, fdRead)
		// errno != 0 → break (treat as EOF)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstBr(body, 2) // depth 2 = $end-of-outer-block
		body = inst.InstEnd(body)

		// if nread == 0 (EOF), break
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)

		// byte = mem[scratch+12]
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstLocalSet(body, 6)

		// Grow buf if cur+1 > buf_size.
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GtS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			body = inst.InstLocalTee(body, 8)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalTee(body, 7)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 5)
			body = memory.InstMemoryCopy(body)
			body = inst.InstLocalGet(body, 7)
			body = inst.InstLocalSet(body, 3)
			body = inst.InstLocalGet(body, 8)
			body = inst.InstLocalSet(body, 4)
		}
		body = inst.InstEnd(body)

		// mem[buf + cur] = byte
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 6)
		body = memory.InstI32Store8(body, 0, 0)
		// cur += 1
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 5)

		// If byte == '\n', break.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, '\n')
		body = numeric.InstI32Eq(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// If no bytes were accumulated, return None.
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 10)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// Materialise the string: alloc cur bytes, copy from buf.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstMemoryCopy(body)

	// Build Some(string): 16 bytes, tag=0, padding, data@8, len@12.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 10)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)

	// 10 i32 locals after the 1 param (slots 1..10).
	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadLineFdBodyP2 is the preview-2 variant of
// buildReaderReadLineFdBody. The Reader holds an input-stream
// handle (stored by __fern_stdin's get-stdin or open_reader's
// read-via-stream) at +0 instead of an fd; each byte comes from
// wasi:io/streams::blocking-read(handle, 1) rather than fd_read.
// disc != 0 (stream-error / closed = EOF) or an empty ok-list ends
// the line. Same growable accumulator + Option[string] box as the
// fd version.
//
// Locals (after 1 param r): 1=retbuf(12), 2=handle, 3=buf,
// 4=buf_size, 5=cur, 6=byte, 7=newbuf, 8=newsize, 9=strbuf, 10=box.
func buildReaderReadLineFdBodyP2(idxs map[string]uint32) []byte {
	// Reused for retbuf/buf scratch AND the strbuf that becomes the
	// returned string data → rc1 for reclamation (over-headering the
	// scratch is harmless carrier-side).
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	blockingRead := idxs["wasi_io_blocking_read"]

	var body []byte
	// retbuf = alloc(12) — result<list<u8>, stream-error>.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	// handle = mem[r]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)
	// accumulator: 64 bytes, doubling.
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstI32Const(body, 64)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// blocking-read(handle, 1, retbuf)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI64Const(body, 1)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstCall(body, blockingRead)
		// disc != 0 → EOF/error → break out of block.
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstBr(body, 2)
		body = inst.InstEnd(body)
		// list len == 0 → break (no byte).
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Load(body, 2, 8)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		// byte = mem8[ mem[retbuf+4] ]  (first byte of the list).
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Load(body, 2, 4)
		body = memory.InstI32Load8U(body, 0, 0)
		body = inst.InstLocalSet(body, 6)
		// Grow if cur+1 > buf_size.
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GtS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			body = inst.InstLocalTee(body, 8)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalTee(body, 7)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 5)
			body = memory.InstMemoryCopy(body)
			body = inst.InstLocalGet(body, 7)
			body = inst.InstLocalSet(body, 3)
			body = inst.InstLocalGet(body, 8)
			body = inst.InstLocalSet(body, 4)
		}
		body = inst.InstEnd(body)
		// mem[buf+cur] = byte; cur++.
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 6)
		body = memory.InstI32Store8(body, 0, 0)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 5)
		// byte == '\n' → break.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, '\n')
		body = numeric.InstI32Eq(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstBr(body, 0)
	}
	// end read loop, end outer block
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)

	// cur == 0 → None.
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 10)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 10)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// Some(string): strbuf = alloc(cur); copy; box(16) tag0 data@8 len@12.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstMemoryCopy(body)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 10)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 9)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 10)

	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadChunkBody — `__method_Reader_read_chunk(r, n)`.
// Single fd_read into an n-byte heap buffer. Returns Some(string)
// for the bytes actually read (possibly < n), None on EOF.
//
// Signature: (r, n: i32) → i32 (heap-form Option[string]).
//
// Locals (after the two params):
//
//	0: $r       (param)
//	1: $n       (param)
//	2: $scratch
//	3: $fd
//	4: $buf
//	5: $nread
//	6: $result
func buildReaderReadChunkBody(idxs map[string]uint32) []byte {
	// $buf (the n-byte chunk) is returned as the Some(chunk) string data
	// → rc1 for reclamation (scratch over-headering is harmless).
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	fdRead := idxs["wasi_fd_read"]

	var body []byte

	// scratch (12 bytes: iov + nread retptr)
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// fd = mem[$r]
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)

	// buf = alloc(n)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4)

	// iov.base = buf
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	// iov.len = n
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Store(body, 2, 0)

	// fd_read(fd, scratch, 1, scratch+8); drop errno (EOF / err → None below)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, fdRead)
	body = inst.InstDrop(body)

	// nread = mem[scratch+8]
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 5)

	// if nread == 0, return None.
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 6)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// Build Some(string): 16 bytes, tag=0, data@8, len@12.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 6)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)

	// 5 i32 locals after the 2 params (slots 2..6).
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadChunkBodyP2 is the preview-2 variant of
// __method_Reader_read_chunk. A single
// wasi:io/streams::blocking-read(handle, n) on the Reader's stored
// stream handle; the host returns the bytes in a freshly
// realloc'd list, so the Some(string) points straight at that
// buffer (no copy). disc != 0 (closed / error) or an empty list
// yields None.
//
// Locals (after 2 params r, n): 2=retbuf(12), 3=handle,
// 4=chunk_ptr, 5=chunk_len, 6=box.
func buildReaderReadChunkBodyP2(idxs map[string]uint32) []byte {
	// chunk buffer returned as Some(chunk) string data → rc1.
	alloc := idxs["__fern_alloc_rc1"]
	allocBox := idxs["__fern_alloc_box"]
	blockingRead := idxs["wasi_io_blocking_read"]

	var body []byte
	// retbuf = alloc(12).
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	// handle = mem[r].
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)
	// blocking-read(handle, (i64)n, retbuf).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = append(body, 0xAD) // i64.extend_i32_u
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, blockingRead)
	// disc != 0 → None.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 6)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// chunk_len = mem[rb+8]; if 0 → None.
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 8)
	body = inst.InstLocalTee(body, 5)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 4)
		body = inst.InstCall(body, allocBox)
		body = inst.InstLocalTee(body, 6)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// chunk_ptr = mem[rb+4].
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 4)
	body = inst.InstLocalSet(body, 4)
	// Some(string): box(16) tag=0 @0, data=chunk_ptr @+8, len @+12.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 6)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)

	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
