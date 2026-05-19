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
	errnoIntr    int32 = 27 // EINTR  → Interrupted
	errnoNoEnt   int32 = 44 // ENOENT → NotFound
	errnoNoTsup  int32 = 58 // ENOTSUP → Unsupported
)

// WASI preview-1 RIGHTS bitset values. We only need
// RIGHT_FD_READ (and inheriting variant) for read_file; write
// support adds RIGHT_FD_WRITE later.
const (
	wasiRightFdRead  int64 = 0x02
	wasiRightFdSeek  int64 = 0x01
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
	alloc := idxs["__lang_alloc"]
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
			b = inst.InstCall(b, alloc)
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
			b = inst.InstCall(b, alloc)
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
	body = inst.InstCall(body, alloc)
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

// buildReadFileBody assembles __lang_read_file.
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
// Path: alloc 16-byte scratch → path_open(fd=3, …, retptr) →
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
func buildReadFileBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	buildIoErr := idxs["__build_io_error"]
	pathOpen := idxs["wasi_path_open"]
	fdRead := idxs["wasi_fd_read"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte

	// scratch = alloc(16)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	// errno = path_open(dirfd=3, dirflags=1, path_data, path_len,
	//                    oflags=0, fs_rights_base=RIGHT_FD_READ,
	//                    fs_rights_inheriting=RIGHT_FD_READ,
	//                    fdflags=0, retptr=scratch)
	body = inst.InstI32Const(body, preopenDirfd)        // dirfd
	body = inst.InstI32Const(body, 1)                   // dirflags (symlink_follow)
	body = inst.InstLocalGet(body, 0)                   // path_data
	body = inst.InstLocalGet(body, 1)                   // path_len
	body = inst.InstI32Const(body, 0)                   // oflags
	body = inst.InstI64Const(body, wasiRightFdRead|wasiRightFdSeek) // fs_rights_base
	body = inst.InstI64Const(body, wasiRightFdRead|wasiRightFdSeek) // fs_rights_inheriting
	body = inst.InstI32Const(body, 0)                   // fdflags
	body = inst.InstLocalGet(body, 2)                   // retptr → scratch[0..3]
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 3) // $errno

	// if errno != 0 { return build_io_error(errno, path_data, path_len) wrapped in Err }
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// Build the IoError variant via __build_io_error;
		// stash it in local 9 (reusing $new_buf — unused on
		// the error path).
		body = inst.InstLocalGet(body, 3) // errno
		body = inst.InstLocalGet(body, 0) // path_data
		body = inst.InstLocalGet(body, 1) // path_len
		body = inst.InstCall(body, buildIoErr)
		body = inst.InstLocalSet(body, 9)
		// Wrap as Err. The IR's payloadLayout for
		// `Result[String, IoError]`'s Err variant places the
		// (pointer-shaped) IoError payload at offset 4 — no
		// 8-byte alignment padding because the slot itself is
		// 4 bytes wide. Total Err allocation is 8 bytes (tag
		// at +0, IoError ptr at +4); Ok stays 16 (string is
		// 8-byte-aligned at +8).
		body = inst.InstI32Const(body, 8)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalTee(body, 12) // $result
		body = inst.InstI32Const(body, 1)  // tag = 1 (Err)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 12)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 9) // IoError ptr
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 12)
		body = inst.InstReturn(body)
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
		body = numeric.InstI32Add(body) // scratch+4 (iov_ptr)
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
		body = append(body, 0xFC, 0x0A, 0x00, 0x00) // memory.copy (src=0, dst=0)

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

	// strbuf = alloc(cur); memory.copy(strbuf, buf, cur)
	body = inst.InstLocalGet(body, 7) // cur
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 11) // $strbuf
	body = inst.InstLocalGet(body, 5)  // buf
	body = inst.InstLocalGet(body, 7)  // cur
	body = append(body, 0xFC, 0x0A, 0x00, 0x00) // memory.copy (src=0, dst=0)

	// Build Ok(string) — 16 bytes: tag=0 @ 0, data @ +8, len @ +12.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
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
	body = inst.InstLocalGet(body, 7) // cur
	body = memory.InstI32Store(body, 2, 0) // len @ +12

	body = inst.InstLocalGet(body, 12)

	// Locals declaration: 11 i32 locals (slots 2..12).
	locals := inst.PutLocalsOneGroup(nil, 11, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
