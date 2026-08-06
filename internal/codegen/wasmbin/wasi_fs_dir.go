// Directory and metadata runtime helpers for the wasmbin backend:
// `remove_file`, `remove_dir_all`, `stat`, `read_dir`, `temp_dir`.
//
// Split from wasi_fs.go, which owns the file-CONTENT helpers
// (read_file / write_file / open_reader / …). These five share that
// file's toolkit — `preopenDirfd`, `emitStrNormalize`,
// `__build_io_error` and its errno map — but nothing else, and they
// are the ones #6208 was about: wasmbin had no filesystem builtins
// beyond one-shot reads and writes, so any program importing
// `std/test` failed to build for wasm with `unknown callee
// "remove_dir_all"` (its `TestRunner.finish` scrubs `cleanup_paths`
// unconditionally, whether or not the suite touched the disk).
//
// PREVIEW-1 ONLY, so far. Every file-I/O helper in wasmbin carries a
// preview-1 body and a preview-2 sibling (`…BodyP2`), because
// `wasmbin.Build` emits a preview-1 core module — what the e2e
// harness runs under `wasmtime --invoke main` — while `bin/fern
// -target wasm` builds with Preview2WASI and composes a component.
// The preview-2 halves of these five are the follow-up; they need
// seven new `wasi:filesystem` imports plus composer wiring, and they
// reuse the Result / FileStat construction pinned here.
//
// Shared shapes, all matching the checker's declarations:
//
//	Result[T, IoError]  8-byte box: tag@0 (0=Ok, 1=Err), payload@4.
//	Ok(())              tag 0, unit payload 0 — same as write_file.
//	FileStat            struct { is_file, is_dir: boolean, size: i32 }
//	                    behind an 8-byte rc sentinel header.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// WASI preview-1 `filetype` values, read from the byte at offset 16
// of the 64-byte filestat record. `stat` only distinguishes these
// two; everything else (sockets, character devices, symlinks that
// SYMLINK_FOLLOW did not resolve) reports neither is_file nor
// is_dir, which is the honest answer for a Fern FileStat.
const (
	wasiFiletypeDirectory int32 = 3
	wasiFiletypeRegular   int32 = 4
)

// Offsets into the preview-1 `filestat` record path_filestat_get
// writes. The record is 64 bytes; these are the two fields `stat`
// surfaces.
const (
	filestatFiletypeOff = 16 // u8
	filestatSizeOff     = 32 // u64
)

// Offsets into a preview-1 `dirent` header, and its size. Each
// header is followed by d_namlen unterminated name bytes.
const (
	direntNamlenOff   = 16 // u32
	direntTypeOff     = 20 // u8
	direntHeaderBytes = 24
)

// WASI preview-1 bits the directory helpers need beyond wasi_fs.go's
// set. OFLAG_DIRECTORY makes path_open refuse a non-directory (so a
// read_dir on a regular file reports ENOTDIR rather than succeeding
// and yielding nothing), and RIGHT_FD_READDIR is bit 14 — without it
// the open succeeds and fd_readdir comes back ENOTCAPABLE.
const (
	wasiOflagDirectory int32 = 0x02
	wasiRightFdReaddir int64 = 0x4000
	// Read + seek + readdir + the right to open and delete beneath
	// this descriptor, which remove_dir_all needs on every level it
	// descends into.
	wasiRightPathUnlink    int64 = 0x4000000
	wasiRightPathRemoveDir int64 = 0x8000000
	wasiRightFdDirRead           = wasiRightFdRead | wasiRightFdSeek |
		wasiRightFdReaddir | wasiRightPathOpen |
		wasiRightPathUnlink | wasiRightPathRemoveDir
)

// readdirBufBytes is the buffer fd_readdir fills. When it comes back
// full the listing may have been truncated, so the helpers double
// and retry rather than paginating by cookie — the whole entry list
// has to be materialised into one array regardless, and a cookie
// walk would re-enter the directory between calls.
const readdirBufBytes = 4096

// emitResultErr appends "build IoError(errno) from the path in
// params 0/1, wrap it in Err, return". The Err box is the same shape
// buildReadFileErr uses (tag=1 @0, IoError ptr @+4); this sibling
// exists because these helpers have their own local numbering rather
// than buildReadFileBodyP2's.
func emitResultErr(body []byte, buildIoErr, allocBox, errnoLocal, errPtrLocal, boxLocal uint32) []byte {
	body = inst.InstLocalGet(body, errnoLocal)
	body = inst.InstLocalGet(body, 0) // path_data
	body = inst.InstLocalGet(body, 1) // path_len
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
	body = inst.InstReturn(body)
	return body
}

// emitResultOkPtr appends "wrap `payloadLocal` in Ok and leave the
// box on the stack". Used for Ok(()) with a 0 payload as well —
// the unit occupies a payload slot like any other value.
func emitResultOkPtr(body []byte, allocBox, payloadLocal, boxLocal uint32) []byte {
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, boxLocal)
	body = inst.InstI32Const(body, 0) // tag = Ok
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, payloadLocal)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, boxLocal)
	return body
}

// buildRemoveFileBody assembles __fern_remove_file.
//
// Signature: (path_data, path_len) → i32 — heap-form
// Result[void, IoError].
//
// path_unlink_file under the fd-3 preopen, then Ok(()) or
// Err(IoError). Removing a non-existent file IS an error here
// (ENOENT → NotFound), matching the checker's documented contract
// and the interpreter's os.Remove.
//
// Locals after the two params:
//
//	2: $path_buf   3: $path_byte_len   4: $i (normalize scratch)
//	5: $errno      6: $err_ptr         7: $box
func buildRemoveFileBody(idxs map[string]uint32) []byte {
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	unlink := idxs["wasi_path_unlink_file"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	// errno = path_unlink_file(preopen, path_buf, path_byte_len)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, unlink)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErr(body, buildIoErr, allocBox, 5, 6, 7)
	}
	body = inst.InstEnd(body)

	// Ok(()) — payload 0 via a zeroed local.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = emitResultOkPtr(body, allocBox, 6, 7)

	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStatBody assembles __fern_stat.
//
// Signature: (path_data, path_len) → i32 — heap-form
// Result[FileStat, IoError].
//
// path_filestat_get with SYMLINK_FOLLOW into a 64-byte record, then
// project filetype and size into the FileStat struct. `size` is
// declared i32 in the checker while WASI reports a u64, so the low
// word is what lands — the same narrowing every other size-carrying
// builtin does, and files past 2 GiB are outside what the rest of
// the string/array surface can hold anyway.
//
// Locals after the two params:
//
//	2: $path_buf  3: $path_byte_len  4: $i  5: $errno
//	6: $stat_buf  7: $filetype       8: $fs (struct data ptr)
//	9: $box
func buildStatBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	filestatGet := idxs["wasi_path_filestat_get"]

	var body []byte
	// The 64-byte record first, so it lands word-aligned before the
	// path normalize advances the cursor by an arbitrary byte count
	// (the same ordering buildWriteFileBody documents for its retptr).
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6)
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	// errno = path_filestat_get(preopen, SYMLINK_FOLLOW, path, len, buf)
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 1) // lookupflags: symlink_follow
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, filestatGet)
	body = inst.InstLocalSet(body, 5)

	body = inst.InstLocalGet(body, 5)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErr(body, buildIoErr, allocBox, 5, 7, 9)
	}
	body = inst.InstEnd(body)

	// filetype = mem8[buf + 16]
	body = inst.InstLocalGet(body, 6)
	body = memory.InstI32Load8U(body, 0, filestatFiletypeOff)
	body = inst.InstLocalSet(body, 7)

	// FileStat: 8-byte rc sentinel header + { is_file, is_dir, size }.
	body = inst.InstI32Const(body, 8+12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 8)
	body = inst.InstI32Const(body, -0x80000000) // static rc sentinel
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 8)

	// is_file = filetype == REGULAR
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, wasiFiletypeRegular)
	body = numeric.InstI32Eq(body)
	body = memory.InstI32Store(body, 2, 0)
	// is_dir = filetype == DIRECTORY
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, wasiFiletypeDirectory)
	body = numeric.InstI32Eq(body)
	body = memory.InstI32Store(body, 2, 4)
	// size = low word of the u64 at +32
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 6)
	body = memory.InstI32Load(body, 2, filestatSizeOff)
	body = memory.InstI32Store(body, 2, 8)

	body = emitResultOkPtr(body, allocBox, 8, 9)

	locals := inst.PutLocalsOneGroup(nil, 8, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildOpenDirBody assembles __fern_open_dir — the shared "open a
// directory for reading" step behind read_dir and remove_dir_all.
//
// Signature: (path_buf, path_byte_len) → i32 — the fd, or
// -(errno) when path_open failed. A NEGATIVE return is the error
// channel because the caller needs the errno to build an IoError,
// and preview-1 errnos are small positives so the sign is free.
//
// Directories need FD_READDIR (bit 14) alongside the read rights;
// without it wasmtime returns ENOTCAPABLE from fd_readdir even
// though the open succeeded.
//
// Locals after the two params: 2: $scratch  3: $errno
func buildOpenDirBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	pathOpen := idxs["wasi_path_open"]

	var body []byte
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)

	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstI32Const(body, 1) // dirflags: symlink_follow
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, wasiOflagDirectory)
	body = inst.InstI64Const(body, wasiRightFdDirRead)
	body = inst.InstI64Const(body, wasiRightFdDirRead)
	body = inst.InstI32Const(body, 0) // fdflags
	body = inst.InstLocalGet(body, 2) // retptr
	body = inst.InstCall(body, pathOpen)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// return -errno
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = numeric.InstI32Sub(body)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Load(body, 2, 0)

	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadDirRawBody assembles __fern_read_dir_raw — fd_readdir into
// a buffer that grows until the listing fits.
//
// Signature: (fd) → i32 — the buffer pointer, with the USED byte
// count stored at buf-4. On error returns -(errno).
//
// A full buffer means the listing may have been truncated, so this
// doubles and retries from cookie 0 rather than paginating: a cookie
// walk would re-enter the directory between calls, and the entry list
// has to be materialised whole anyway.
//
// Locals after the param: 1: $cap  2: $buf  3: $used_ptr
// 4: $errno  5: $used
func buildReadDirRawBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	readdir := idxs["wasi_fd_readdir"]

	var body []byte
	// used_ptr first, so it is word-aligned for the host's u32 write.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstI32Const(body, readdirBufBytes)
	body = inst.InstLocalSet(body, 1)

	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// buf = alloc(cap + 4), data at buf+4 so the used count can
		// live at buf-4 relative to the returned pointer.
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)

		// errno = fd_readdir(fd, buf, cap, cookie=0, used_ptr)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI64Const(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, readdir)
		body = inst.InstLocalTee(body, 4)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstI32Const(body, 0)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Sub(body)
			body = inst.InstReturn(body)
		}
		body = inst.InstEnd(body)

		// used = mem[used_ptr]; if used < cap the listing is complete.
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, 5)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32LtU(body)
		body = inst.InstBrIf(body, 1)

		// Truncated: double and retry.
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 2)
		body = numeric.InstI32Mul(body)
		body = inst.InstLocalSet(body, 1)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// mem[buf-4] = used; return buf.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 2)

	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitIsDotName appends a test for the "." / ".." entries, leaving
// 1 on the stack when the name at (nameLocal, namlenLocal) is one of
// them. WASI's fd_readdir yields both; Go's os.ReadDir — which the
// interpreter wraps — does not, so read_dir must drop them or the
// two backends disagree on every directory listing.
func emitIsDotName(body []byte, nameLocal, namlenLocal uint32) []byte {
	// namlen == 1 && name[0] == '.'
	body = inst.InstLocalGet(body, namlenLocal)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, nameLocal)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstI32Const(body, '.')
	body = numeric.InstI32Eq(body)
	body = numeric.InstI32And(body)
	// || (namlen == 2 && name[0] == '.' && name[1] == '.')
	body = inst.InstLocalGet(body, namlenLocal)
	body = inst.InstI32Const(body, 2)
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, nameLocal)
	body = memory.InstI32Load8U(body, 0, 0)
	body = inst.InstI32Const(body, '.')
	body = numeric.InstI32Eq(body)
	body = numeric.InstI32And(body)
	body = inst.InstLocalGet(body, nameLocal)
	body = memory.InstI32Load8U(body, 0, 1)
	body = inst.InstI32Const(body, '.')
	body = numeric.InstI32Eq(body)
	body = numeric.InstI32And(body)
	body = numeric.InstI32Or(body)
	return body
}

// buildReadDirBody assembles __fern_read_dir.
//
// Signature: (path_data, path_len) → i32 — heap-form
// Result[string[], IoError].
//
// Opens the directory, reads the whole listing, then walks the packed
// dirent records twice: once to count the entries that survive the
// "." / ".." filter, once to fill the array. Two passes rather than a
// growable array because the count decides the single allocation, and
// the buffer is already in hand.
//
// Entries are base names, unsorted — the checker's documented
// contract, and what the interpreter's os.ReadDir yields.
//
// Locals after the two params:
//
//	2: $path_buf  3: $path_byte_len  4: $i      5: $fd
//	6: $buf       7: $used           8: $off    9: $namlen
//	10: $name     11: $count         12: $arr_raw
//	13: $arr      14: $errno         15: $err_ptr  16: $box
func buildReadDirBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	openDir := idxs["__fern_open_dir"]
	readRaw := idxs["__fern_read_dir_raw"]
	strCopy := idxs["__fern_str_copy"]
	fdClose := idxs["wasi_fd_close"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	// fd = __fern_open_dir(path_buf, path_byte_len); negative = -errno.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, openDir)
	body = inst.InstLocalTee(body, 5)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 14)
		body = emitResultErr(body, buildIoErr, allocBox, 14, 15, 16)
	}
	body = inst.InstEnd(body)

	// buf = __fern_read_dir_raw(fd); negative = -errno.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, readRaw)
	body = inst.InstLocalTee(body, 6)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstCall(body, fdClose)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalSet(body, 14)
		body = emitResultErr(body, buildIoErr, allocBox, 14, 15, 16)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, fdClose)
	body = inst.InstDrop(body)

	// used = mem[buf-4]
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 7)

	// Pass 1: count entries, skipping "." and "..".
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 8) // off
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11) // count
	body = emitDirentWalk(body, 6, 7, 8, 9, 10, func(b []byte) []byte {
		b = emitIsDotName(b, 10, 9)
		b = numeric.InstI32Eqz(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		b = inst.InstLocalGet(b, 11)
		b = inst.InstI32Const(b, 1)
		b = numeric.InstI32Add(b)
		b = inst.InstLocalSet(b, 11)
		b = inst.InstEnd(b)
		return b
	})

	// arr_raw = alloc(count*8 + 4); mem[arr_raw] = count; arr = arr_raw+4.
	body = inst.InstLocalGet(body, 11)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Mul(body)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 12)
	body = inst.InstLocalGet(body, 11)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 12)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 13)

	// Pass 2: fill. Reuse local 11 as the write index.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 11)
	body = emitDirentWalk(body, 6, 7, 8, 9, 10, func(b []byte) []byte {
		b = emitIsDotName(b, 10, 9)
		b = numeric.InstI32Eqz(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		{
			// slot = arr + idx*8
			b = inst.InstLocalGet(b, 13)
			b = inst.InstLocalGet(b, 11)
			b = inst.InstI32Const(b, 8)
			b = numeric.InstI32Mul(b)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalSet(b, 14) // reuse errno local as slot ptr
			// (data, len) = __fern_str_copy(name, namlen) — an owned
			// copy, because `buf` is scratch this helper drops.
			b = inst.InstLocalGet(b, 10)
			b = inst.InstLocalGet(b, 9)
			b = inst.InstCall(b, strCopy)
			b = inst.InstLocalSet(b, 15) // len (top of stack)
			b = inst.InstLocalSet(b, 16) // data
			b = inst.InstLocalGet(b, 14)
			b = inst.InstLocalGet(b, 16)
			b = memory.InstI32Store(b, 2, 0)
			b = inst.InstLocalGet(b, 14)
			b = inst.InstLocalGet(b, 15)
			b = memory.InstI32Store(b, 2, 4)

			b = inst.InstLocalGet(b, 11)
			b = inst.InstI32Const(b, 1)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalSet(b, 11)
		}
		b = inst.InstEnd(b)
		return b
	})

	body = emitResultOkPtr(body, allocBox, 13, 16)

	locals := inst.PutLocalsOneGroup(nil, 15, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// emitDirentWalk appends a loop over the packed dirent records in
// [bufLocal, bufLocal+usedLocal), calling `perEntry` with namlenLocal
// and nameLocal set for each. Stops early on a truncated tail — a
// record whose header or name runs past `used` is a partial entry
// fd_readdir could not fit, and reading it would walk off the buffer.
func emitDirentWalk(body []byte, bufLocal, usedLocal, offLocal, namlenLocal, nameLocal uint32, perEntry func([]byte) []byte) []byte {
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if off + header > used: break
		body = inst.InstLocalGet(body, offLocal)
		body = inst.InstI32Const(body, direntHeaderBytes)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, usedLocal)
		body = numeric.InstI32GtU(body)
		body = inst.InstBrIf(body, 1)

		// namlen = mem[buf + off + 16]
		body = inst.InstLocalGet(body, bufLocal)
		body = inst.InstLocalGet(body, offLocal)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, direntNamlenOff)
		body = inst.InstLocalSet(body, namlenLocal)
		// name = buf + off + header
		body = inst.InstLocalGet(body, bufLocal)
		body = inst.InstLocalGet(body, offLocal)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, direntHeaderBytes)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, nameLocal)

		// if off + header + namlen > used: break (truncated tail)
		body = inst.InstLocalGet(body, offLocal)
		body = inst.InstI32Const(body, direntHeaderBytes)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, namlenLocal)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, usedLocal)
		body = numeric.InstI32GtU(body)
		body = inst.InstBrIf(body, 1)

		body = perEntry(body)

		// off += header + namlen
		body = inst.InstLocalGet(body, offLocal)
		body = inst.InstI32Const(body, direntHeaderBytes)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, namlenLocal)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, offLocal)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	return body
}

// buildRmdirRecBody assembles __fern_rmdir_rec — the recursive worker
// behind remove_dir_all.
//
// Signature: (path_buf, path_byte_len) → errno (0 on success). Takes
// a RAW byte path rather than an SSO string so the recursion does not
// re-normalize at every level.
//
// Drains the directory (recursing into subdirectories, unlinking
// everything else) and then removes the now-empty directory itself.
// The dispatch is on the dirent's d_type: preview-1 reports the entry
// kind inline, so no per-child stat is needed.
//
// Locals after the two params:
//
//	2: $fd    3: $buf      4: $used   5: $off
//	6: $namlen 7: $name    8: $child  9: $child_len
//	10: $i    11: $errno
func buildRmdirRecBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	openDir := idxs["__fern_open_dir"]
	readRaw := idxs["__fern_read_dir_raw"]
	unlink := idxs["wasi_path_unlink_file"]
	rmdir := idxs["wasi_path_remove_directory"]
	fdClose := idxs["wasi_fd_close"]
	self := idxs["__fern_rmdir_rec"]

	var body []byte
	// fd = open_dir(path); negative = -errno.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, openDir)
	body = inst.InstLocalTee(body, 2)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Sub(body)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)

	// buf = read_dir_raw(fd); negative = -errno.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, readRaw)
	body = inst.InstLocalTee(body, 3)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, fdClose)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = numeric.InstI32Sub(body)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// Close before descending: each level opens its own fd, and
	// wasmtime's fd table is small enough that holding one per level
	// would cap the recursion depth for no benefit — the listing is
	// already copied into `buf`.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, fdClose)
	body = inst.InstDrop(body)

	// used = mem[buf-4]
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 5)
	body = emitDirentWalk(body, 3, 4, 5, 6, 7, func(b []byte) []byte {
		b = emitIsDotName(b, 7, 6)
		b = numeric.InstI32Eqz(b)
		b = inst.InstIfStart(b, inst.BlocktypeEmpty)
		{
			// child = parent + "/" + name
			b = inst.InstLocalGet(b, 1)
			b = inst.InstI32Const(b, 1)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalGet(b, 6)
			b = numeric.InstI32Add(b)
			b = inst.InstLocalTee(b, 9)
			b = inst.InstCall(b, alloc)
			b = inst.InstLocalSet(b, 8)
			// copy parent
			b = inst.InstI32Const(b, 0)
			b = inst.InstLocalSet(b, 10)
			b = inst.InstBlockStart(b, inst.BlocktypeEmpty)
			b = inst.InstLoopStart(b, inst.BlocktypeEmpty)
			{
				b = inst.InstLocalGet(b, 10)
				b = inst.InstLocalGet(b, 1)
				b = numeric.InstI32GeU(b)
				b = inst.InstBrIf(b, 1)
				b = inst.InstLocalGet(b, 8)
				b = inst.InstLocalGet(b, 10)
				b = numeric.InstI32Add(b)
				b = inst.InstLocalGet(b, 0)
				b = inst.InstLocalGet(b, 10)
				b = numeric.InstI32Add(b)
				b = memory.InstI32Load8U(b, 0, 0)
				b = memory.InstI32Store8(b, 0, 0)
				b = inst.InstLocalGet(b, 10)
				b = inst.InstI32Const(b, 1)
				b = numeric.InstI32Add(b)
				b = inst.InstLocalSet(b, 10)
				b = inst.InstBr(b, 0)
			}
			b = inst.InstEnd(b)
			b = inst.InstEnd(b)
			// separator
			b = inst.InstLocalGet(b, 8)
			b = inst.InstLocalGet(b, 1)
			b = numeric.InstI32Add(b)
			b = inst.InstI32Const(b, '/')
			b = memory.InstI32Store8(b, 0, 0)
			// copy name
			b = inst.InstI32Const(b, 0)
			b = inst.InstLocalSet(b, 10)
			b = inst.InstBlockStart(b, inst.BlocktypeEmpty)
			b = inst.InstLoopStart(b, inst.BlocktypeEmpty)
			{
				b = inst.InstLocalGet(b, 10)
				b = inst.InstLocalGet(b, 6)
				b = numeric.InstI32GeU(b)
				b = inst.InstBrIf(b, 1)
				b = inst.InstLocalGet(b, 8)
				b = inst.InstLocalGet(b, 1)
				b = numeric.InstI32Add(b)
				b = inst.InstI32Const(b, 1)
				b = numeric.InstI32Add(b)
				b = inst.InstLocalGet(b, 10)
				b = numeric.InstI32Add(b)
				b = inst.InstLocalGet(b, 7)
				b = inst.InstLocalGet(b, 10)
				b = numeric.InstI32Add(b)
				b = memory.InstI32Load8U(b, 0, 0)
				b = memory.InstI32Store8(b, 0, 0)
				b = inst.InstLocalGet(b, 10)
				b = inst.InstI32Const(b, 1)
				b = numeric.InstI32Add(b)
				b = inst.InstLocalSet(b, 10)
				b = inst.InstBr(b, 0)
			}
			b = inst.InstEnd(b)
			b = inst.InstEnd(b)

			// Directory → recurse; anything else → unlink.
			b = inst.InstLocalGet(b, 3)
			b = inst.InstLocalGet(b, 5)
			b = numeric.InstI32Add(b)
			b = memory.InstI32Load8U(b, 0, direntTypeOff)
			b = inst.InstI32Const(b, wasiFiletypeDirectory)
			b = numeric.InstI32Eq(b)
			b = inst.InstIfStart(b, inst.BlocktypeEmpty)
			{
				b = inst.InstLocalGet(b, 8)
				b = inst.InstLocalGet(b, 9)
				b = inst.InstCall(b, self)
				b = inst.InstLocalTee(b, 11)
				b = inst.InstIfStart(b, inst.BlocktypeEmpty)
				b = inst.InstLocalGet(b, 11)
				b = inst.InstReturn(b)
				b = inst.InstEnd(b)
			}
			b = inst.InstElse(b)
			{
				b = inst.InstI32Const(b, preopenDirfd)
				b = inst.InstLocalGet(b, 8)
				b = inst.InstLocalGet(b, 9)
				b = inst.InstCall(b, unlink)
				b = inst.InstLocalTee(b, 11)
				b = inst.InstIfStart(b, inst.BlocktypeEmpty)
				b = inst.InstLocalGet(b, 11)
				b = inst.InstReturn(b)
				b = inst.InstEnd(b)
			}
			b = inst.InstEnd(b)
		}
		b = inst.InstEnd(b)
		return b
	})

	// The directory is empty now — remove it.
	body = inst.InstI32Const(body, preopenDirfd)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, rmdir)

	locals := inst.PutLocalsOneGroup(nil, 10, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRemoveDirAllBody assembles __fern_remove_dir_all.
//
// Signature: (path_data, path_len) → i32 — heap-form
// Result[void, IoError].
//
// Normalizes the path once and hands off to __fern_rmdir_rec. A
// MISSING directory is Ok(()), not Err — Go's os.RemoveAll semantics,
// which the checker documents and `std/test`'s cleanup relies on
// (it scrubs every registered path whether or not the test created
// it).
//
// Locals after the two params:
//
//	2: $path_buf  3: $path_byte_len  4: $i
//	5: $errno     6: $err_ptr        7: $box
func buildRemoveDirAllBody(idxs map[string]uint32) []byte {
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	rec := idxs["__fern_rmdir_rec"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, rec)
	body = inst.InstLocalSet(body, 5)

	// errno != 0 && errno != ENOENT → Err.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, errnoNoEnt)
	body = numeric.InstI32Ne(body)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErr(body, buildIoErr, allocBox, 5, 6, 7)
	}
	body = inst.InstEnd(body)

	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = emitResultOkPtr(body, allocBox, 6, 7)

	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildTempDirBody assembles __fern_temp_dir.
//
// Signature: (prefix_data, prefix_len) → i32 — heap-form
// Result[string, IoError] carrying the CREATED directory's path.
//
// The interpreter is os.MkdirTemp("", prefix+"-*"), so this creates a
// uniquely-named directory rather than reporting $TMPDIR — there is
// no preview-1 mkdtemp, so the uniqueness is ours: draw a suffix from
// the same CSPRNG `__fern_random_i32` uses and retry while
// path_create_directory reports EEXIST.
//
// The path is relative to the fd-3 preopen rather than absolute:
// preview-1 has no notion of an absolute filesystem here, and every
// other path builtin in this backend is preopen-relative, so an
// absolute /tmp path would be unusable by the read_file / write_file
// the caller goes on to use.
//
// Locals after the two params:
//
//	2: $pfx_buf  3: $pfx_len  4: $i     5: $buf
//	6: $len      7: $rnd      8: $attempt  9: $errno
//	10: $err_ptr 11: $box     12: $data 13: $slen
func buildTempDirBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	allocBox := idxs["__fern_alloc_box"]
	buildIoErr := idxs["__build_io_error"]
	mkdir := idxs["wasi_path_create_directory"]
	random := idxs["__fern_random_i32"]
	strCopy := idxs["__fern_str_copy"]

	var body []byte
	body = emitStrNormalize(body, idxs, 0, 1, 2, 3, 4)

	// buf = alloc(pfx_len + 1 + 8); len = pfx_len + 9.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 9)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalTee(body, 6)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 5)
	// Copy the prefix in once; only the 8 hex digits change per try.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = memory.InstI32Store8(body, 0, 0)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	// separator
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 3)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, '-')
	body = memory.InstI32Store8(body, 0, 0)

	// Retry loop: 64 attempts, then give up with the last errno.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 8)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 8)
		body = inst.InstI32Const(body, 64)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)

		// Eight hex digits from one random word.
		body = inst.InstCall(body, random)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32GeU(body)
			body = inst.InstBrIf(body, 1)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 3)
			body = numeric.InstI32Add(body)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Add(body)
			// nibble = (rnd >> (i*4)) & 15, then hex-encode it
			// branch-free: '0'+n, plus 39 more once n >= 10, which
			// is exactly the '0'..'9' → 'a'..'f' gap.
			body = inst.InstLocalGet(body, 7)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 15)
			body = numeric.InstI32And(body)
			body = inst.InstLocalSet(body, 9)
			body = inst.InstLocalGet(body, 9)
			body = inst.InstI32Const(body, '0')
			body = numeric.InstI32Add(body)
			body = inst.InstLocalGet(body, 9)
			body = inst.InstI32Const(body, 10)
			body = numeric.InstI32GeU(body)
			body = inst.InstI32Const(body, 'a'-'0'-10)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Store8(body, 0, 0)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 4)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body)
		body = inst.InstEnd(body)

		// errno = path_create_directory(preopen, buf, len)
		body = inst.InstI32Const(body, preopenDirfd)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstCall(body, mkdir)
		body = inst.InstLocalTee(body, 9)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1) // created → done

		// Only EEXIST is worth another name; anything else is fatal.
		body = inst.InstLocalGet(body, 9)
		body = inst.InstI32Const(body, errnoExist)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = emitResultErr(body, buildIoErr, allocBox, 9, 10, 11)
		}
		body = inst.InstEnd(body)

		body = inst.InstLocalGet(body, 8)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 8)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block

	// Exhausted the attempts without creating anything.
	body = inst.InstLocalGet(body, 9)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = emitResultErr(body, buildIoErr, allocBox, 9, 10, 11)
	}
	body = inst.InstEnd(body)

	// Ok(path) — an owned copy, since `buf` is this helper's scratch.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstCall(body, strCopy)
	body = inst.InstLocalSet(body, 13) // len
	body = inst.InstLocalSet(body, 12) // data
	// Ok(string) is a 16-byte box — tag@0, then the two-word string at
	// its 8-aligned payload slot (data@+8, len@+12). NOT the 8-byte
	// single-word shape emitResultOkPtr builds for a pointer payload;
	// read_file's Ok is the reference.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 11)
	body = inst.InstI32Const(body, 0) // tag = Ok
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 11)
	body = inst.InstLocalGet(body, 12)
	body = memory.InstI32Store(body, 2, 8)
	body = inst.InstLocalGet(body, 11)
	body = inst.InstLocalGet(body, 13)
	body = memory.InstI32Store(body, 2, 12)
	body = inst.InstLocalGet(body, 11)

	locals := inst.PutLocalsOneGroup(nil, 12, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
