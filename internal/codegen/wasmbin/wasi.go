// Imports + WASI-facing helpers for the wasmbin backend.
//
// The lang `print(s)` lowering eventually calls a synthetic
// __lang_print helper. The helper takes a (data, len) string,
// normalises it to a heap buffer (so inline-form strings work
// via the SSO seam), writes a single iovec to a fixed scratch
// region of linear memory, and invokes the imported WASI
// preview-1 fd_write.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// importSpec describes one imported function.
type importSpec struct {
	module  string
	name    string
	params  []byte
	results []byte
}

// importSpecs is the import registry. Each entry corresponds to
// one wasi_snapshot_preview1 (or similar) imported function.
var importSpecs = map[string]importSpec{
	"wasi_fd_write": {
		// (fd, iovs_ptr, iovs_count, nwritten_ptr) → errno
		module:  "wasi_snapshot_preview1",
		name:    "fd_write",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_proc_exit": {
		// (exit_code: i32) → ! (never returns; the wasi spec
		// marks this as a "trap-like" abrupt termination, but
		// the wasm-level signature still says void return).
		module:  "wasi_snapshot_preview1",
		name:    "proc_exit",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_random_get": {
		// (buf_ptr, buf_len) → errno. Fills buf_ptr..+buf_len
		// with cryptographically-strong random bytes (per the
		// wasi spec; host may degrade in sandboxed environments).
		module:  "wasi_snapshot_preview1",
		name:    "random_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_clock_time_get": {
		// (clock_id i32, precision i64, time_ptr i32) → errno i32.
		// Writes the current time as nanoseconds-since-epoch
		// (u64 little-endian) at time_ptr.
		module:  "wasi_snapshot_preview1",
		name:    "clock_time_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_environ_sizes_get": {
		// (envc_ptr i32, env_buf_size_ptr i32) → errno.
		// Writes the environment-variable count + the total
		// byte size of the concatenated env strings into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "environ_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_sizes_get": {
		// (argc_ptr i32, argv_buf_size_ptr i32) → errno.
		// Writes argv-count + total byte length of the
		// concatenated argv strings (NUL-separated) into the
		// two output pointers.
		module:  "wasi_snapshot_preview1",
		name:    "args_sizes_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_environ_get": {
		// (environ_ptr i32, environ_buf i32) → errno. Writes
		// argc i32 pointers into environ_ptr, followed by the
		// NUL-terminated "KEY=VALUE" strings into environ_buf.
		module:  "wasi_snapshot_preview1",
		name:    "environ_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_args_get": {
		// (argv_ptr i32, argv_buf i32) → errno. Writes argc i32
		// pointers into argv_ptr, followed by the NUL-terminated
		// argv strings into argv_buf.
		module:  "wasi_snapshot_preview1",
		name:    "args_get",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_fd_read": {
		// (fd, iovs_ptr, iovs_count, nread_ptr) → errno.
		// Reads up to sum(iov_len) bytes into the iovs[i].base
		// buffers. nread_ptr is written with the actual bytes
		// read; short reads / EOF set nread to less than the
		// requested total.
		module:  "wasi_snapshot_preview1",
		name:    "fd_read",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_path_open": {
		// (dirfd, dirflags, path_ptr, path_len, oflags,
		//  fs_rights_base i64, fs_rights_inheriting i64,
		//  fdflags, retptr_newfd) → errno.
		// Opens a file under a preopened directory descriptor
		// (`dirfd`); the path is interpreted relative to that
		// descriptor. `retptr_newfd` is written with the new
		// file descriptor on success.
		module: "wasi_snapshot_preview1",
		name:   "path_open",
		params: []byte{
			encode.ValtypeI32, // dirfd
			encode.ValtypeI32, // dirflags
			encode.ValtypeI32, // path_ptr
			encode.ValtypeI32, // path_len
			encode.ValtypeI32, // oflags
			encode.ValtypeI64, // fs_rights_base
			encode.ValtypeI64, // fs_rights_inheriting
			encode.ValtypeI32, // fdflags
			encode.ValtypeI32, // retptr_newfd
		},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_fd_close": {
		// (fd) → errno. Closes the file descriptor. Errors are
		// typically ignored — there's nothing useful for the
		// caller to do with them on close.
		module:  "wasi_snapshot_preview1",
		name:    "fd_close",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},

	// ---- wasi:sockets imports for the TCP helpers ----
	//
	// User-facing surface: tcp_listen / tcp_accept / tcp_recv /
	// tcp_send / tcp_close. The listener / connection structs each
	// live as a 12-byte heap allocation (tcp_socket, input_stream,
	// output_stream); listener sockets zero the stream slots. The
	// host doesn't need `wasmtime --tcp-listen=…` — the guest binds
	// the port itself via wasi:sockets.
	"wasi_sockets_instance_network": {
		// () → network handle. Used once per program; the
		// __network_handle accessor caches the result in low memory.
		module:  "wasi:sockets/instance-network@0.2.0",
		name:    "instance-network",
		params:  nil,
		results: []byte{encode.ValtypeI32},
	},
	"wasi_sockets_create_tcp_socket": {
		// (family: i32, retptr: i32). family=0 → ipv4. retptr
		// gets `result<tcp-socket, error-code>` (disc @ +0, payload
		// at +4 — either the socket handle (Ok) or the error-code
		// byte (Err)).
		module:  "wasi:sockets/tcp-create-socket@0.2.0",
		name:    "create-tcp-socket",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_start_bind": {
		// start-bind takes the canonical-ABI flattening of
		// `ip-socket-address` — a 1-i32 discriminant plus an
		// 11-i32 max payload (ipv4 uses 5 slots, ipv6 needs 11,
		// the variant joins them). Total params: self,
		// borrow<network>, disc, 11 flat slots, retptr = 15 i32.
		module: "wasi:sockets/tcp@0.2.0",
		name:   "[method]tcp-socket.start-bind",
		params: []byte{
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
			encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32,
		},
		results: nil,
	},
	"wasi_sockets_tcp_finish_bind": {
		// (self, retptr) → (). Result<_, error-code> written at retptr.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.finish-bind",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_start_listen": {
		// (self, retptr) → ().
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.start-listen",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_finish_listen": {
		// (self, retptr) → ().
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.finish-listen",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_accept": {
		// (self, retptr) → (). retptr holds
		// `result<tuple<tcp-socket, input-stream, output-stream>,
		// error-code>`: 1 disc byte at +0, 3 bytes pad, then the
		// (sock, in, out) tuple at +4 (Ok) or the error-code byte
		// at +4 (Err). Total payload area: 12 bytes; allocate 16.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.accept",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
	"wasi_sockets_tcp_subscribe": {
		// (self) → pollable handle. Paired with pollable.block to
		// wait until a connection is ready before calling accept.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[method]tcp-socket.subscribe",
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
	},
	"wasi_sockets_tcp_socket_drop": {
		// (handle) → (). Drops a tcp-socket. Must come AFTER any
		// child input-stream / output-stream drops — the canonical
		// ABI rejects parent-drop with live children.
		module:  "wasi:sockets/tcp@0.2.0",
		name:    "[resource-drop]tcp-socket",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_pollable_block": {
		// (pollable) → (). Synchronously waits for the pollable
		// to become ready. Used by tcp_accept before invoking
		// the non-blocking accept.
		module:  "wasi:io/poll@0.2.0",
		name:    "[method]pollable.block",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_pollable_drop": {
		// (pollable) → (). Drops a pollable handle.
		module:  "wasi:io/poll@0.2.0",
		name:    "[resource-drop]pollable",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_input_stream_drop": {
		// (handle) → (). Drops an input-stream resource. Required
		// before dropping the parent tcp-socket (canonical-ABI
		// resource-with-children rule).
		module:  "wasi:io/streams@0.2.0",
		name:    "[resource-drop]input-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_output_stream_drop": {
		// (handle) → (). Drops an output-stream resource. Same
		// child-before-parent rule as input-stream.
		module:  "wasi:io/streams@0.2.0",
		name:    "[resource-drop]output-stream",
		params:  []byte{encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_blocking_read": {
		// (handle: i32, len: u64, retptr: i32) → (). Reads up to
		// `len` bytes. retptr holds result<list<u8>, stream-error>:
		// 1 disc byte @ +0, 3 bytes pad, list-data ptr @ +4, list
		// length @ +8. Allocate 12 bytes for the retptr.
		module:  "wasi:io/streams@0.2.0",
		name:    "[method]input-stream.blocking-read",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64, encode.ValtypeI32},
		results: nil,
	},
	"wasi_io_blocking_write_and_flush": {
		// (handle, ptr, len, retptr) → (). Writes `len` bytes from
		// `ptr` and flushes. retptr holds result<_, stream-error>
		// (disc byte @ +0). Wasmtime enforces a 4 KiB per-call cap
		// — callers loop.
		module:  "wasi:io/streams@0.2.0",
		name:    "[method]output-stream.blocking-write-and-flush",
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
	},
}

// importNeeds is parallel to runtimeNeeds but for imports.
type importNeeds struct {
	order []string
	set   map[string]bool
}

func (in *importNeeds) add(name string) {
	if in.set == nil {
		in.set = map[string]bool{}
	}
	if in.set[name] {
		return
	}
	in.set[name] = true
	in.order = append(in.order, name)
}

// scanImports decides which imports the module needs based on
// the helpers in use (and direct IR-op references in a future
// expansion). Each helper that wraps a WASI call adds its import
// here.
func scanImports(prog *ir.Program, helpers runtimeNeeds) importNeeds {
	var in importNeeds
	if helpers.set["__lang_print"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_eprint"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_write"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_putchar"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_exit"] {
		in.add("wasi_proc_exit")
	}
	if helpers.set["__lang_random_i32"] {
		in.add("wasi_random_get")
	}
	if helpers.set["__lang_random_bytes"] {
		in.add("wasi_random_get")
	}
	if helpers.set["__lang_now_ns"] {
		in.add("wasi_clock_time_get")
	}
	if helpers.set["__lang_now_unix_ms"] {
		in.add("wasi_clock_time_get")
	}
	if helpers.set["__lang_monotonic_ns"] {
		in.add("wasi_clock_time_get")
	}
	if helpers.set["__lang_env_count"] {
		in.add("wasi_environ_sizes_get")
	}
	if helpers.set["__lang_arg_count"] {
		in.add("wasi_args_sizes_get")
	}
	if helpers.set["__lang_arg_at"] {
		in.add("wasi_args_sizes_get")
		in.add("wasi_args_get")
	}
	if helpers.set["__lang_args"] {
		in.add("wasi_args_sizes_get")
		in.add("wasi_args_get")
	}
	if helpers.set["__lang_env_at"] {
		in.add("wasi_environ_sizes_get")
		in.add("wasi_environ_get")
	}
	if helpers.set["__lang_env"] {
		in.add("wasi_environ_sizes_get")
		in.add("wasi_environ_get")
	}
	if helpers.set["__lang_read_byte"] {
		in.add("wasi_fd_read")
	}
	if helpers.set["__lang_read_file"] {
		in.add("wasi_path_open")
		in.add("wasi_fd_read")
		in.add("wasi_fd_close")
	}
	if helpers.set["__lang_write_file"] {
		in.add("wasi_path_open")
		in.add("wasi_fd_write")
		in.add("wasi_fd_close")
	}
	if helpers.set["__lang_open_reader"] ||
		helpers.set["__lang_open_writer"] ||
		helpers.set["__lang_open_appender"] {
		in.add("wasi_path_open")
	}
	if helpers.set["__lang_reader_read_line_fd"] ||
		helpers.set["__lang_reader_read_chunk"] {
		in.add("wasi_fd_read")
	}
	if helpers.set["__lang_writer_write"] {
		in.add("wasi_fd_write")
	}
	if helpers.set["__lang_reader_close_fd"] ||
		helpers.set["__lang_writer_close"] {
		in.add("wasi_fd_close")
	}

	// TCP helpers. Each user-facing builtin (tcp_listen / tcp_accept /
	// tcp_recv / tcp_send / tcp_close) pulls in a different subset of
	// the wasi:sockets + wasi:io imports; we union them here so the
	// transitive close picks up everything needed.
	//
	// __network_handle (the lazy accessor over instance-network) is
	// pulled in by tcp_listen since that's the only call site that
	// needs the network borrow. The others reach for the cached
	// handle slot directly through the accessor.
	if helpers.set["__lang_tcp_listen"] {
		in.add("wasi_sockets_instance_network")
		in.add("wasi_sockets_create_tcp_socket")
		in.add("wasi_sockets_tcp_start_bind")
		in.add("wasi_sockets_tcp_finish_bind")
		in.add("wasi_sockets_tcp_start_listen")
		in.add("wasi_sockets_tcp_finish_listen")
	}
	if helpers.set["__lang_tcp_accept"] {
		in.add("wasi_sockets_tcp_accept")
		in.add("wasi_sockets_tcp_subscribe")
		in.add("wasi_io_pollable_block")
		in.add("wasi_io_pollable_drop")
	}
	if helpers.set["__lang_tcp_recv"] {
		in.add("wasi_io_blocking_read")
	}
	if helpers.set["__lang_tcp_send"] {
		in.add("wasi_io_blocking_write_and_flush")
	}
	if helpers.set["__lang_tcp_close"] {
		in.add("wasi_sockets_tcp_socket_drop")
		in.add("wasi_io_input_stream_drop")
		in.add("wasi_io_output_stream_drop")
	}
	return in
}

// printIovecAddr is the fixed scratch location in linear memory
// where __lang_print writes the iovec (iov_base, iov_len) pair
// before calling fd_write. 8 bytes total; lives outside the
// allocator's region (which starts at 64 by default, here we
// pick 48..56 in the reserved low-memory window before the
// cursor at 40 and the runtime-reserved area up to 64).
const printIovecAddr = 48

// printRetAddr is the 4-byte scratch where fd_write writes the
// nwritten result.
const printRetAddr = 56

// randomBufAddr is the 4-byte scratch where wasi_random_get
// writes the random bytes consumed by __lang_random_i32. Lives
// in the reserved low-memory window past printRetAddr.
const randomBufAddr = 60

// Cache for __lang_arg_at / __lang_env_at. Both helpers lazily
// initialise on first call: ask the host for sizes, alloc the
// pointer table + string buffer, call args_get / environ_get,
// store the (count, table_ptr) in the cache. Subsequent calls
// short-circuit on the init flag and walk the cached table.
// Lives in the low-memory window 0..39 which was previously
// unused (allocCursorAddr starts at 40).
//
//	 0..3   args_init flag (0 / 1)
//	 4..7   args count (i32)
//	 8..11  argv_ptrs heap pointer
//	12..15  args sizes scratch slot 0 (argc out from args_sizes_get)
//	16..19  args sizes scratch slot 1 (bufsize out from args_sizes_get)
//	20..23  env_init flag (0 / 1)
//	24..27  env count (i32)
//	28..31  environ_ptrs heap pointer
//	32..35  env sizes scratch slot 0
//	36..39  env sizes scratch slot 1
const (
	argsInitAddr      = 0
	argsCountAddr     = 4
	argsPtrsAddr      = 8
	argsSizesArgcAddr = 12
	argsSizesBufAddr  = 16
	envInitAddr       = 20
	envCountAddr      = 24
	envPtrsAddr       = 28
	envSizesArgcAddr  = 32
	envSizesBufAddr   = 36
)

// readByteScratchAddr holds the heap-pointer to __lang_read_byte's
// per-call scratch region (iovec + 1-byte buffer + nread out). 0
// means uninitialised; the helper allocs 16 bytes on first call
// and writes the addr here so subsequent calls reuse the same
// region. Lives at 44..47.
const readByteScratchAddr = 44

// strIdxScratchAddr is the 8-byte spill region __str_idx uses
// for inline-form strings: byte 0..3 hold base_data, byte 4..7
// hold base_len, and __str_idx returns scratch+i so the caller's
// OpLoadByte reads the correct content byte. Heap-form strings
// bypass the scratch entirely (returned address is base_data+i).
// Lives at 64..71; closuresBase is set to 80 to leave this room.
const strIdxScratchAddr = 64

// networkHandleInitAddr / networkHandleAddr cache the
// wasi:sockets/instance-network borrow consumed by tcp_listen's
// start-bind step. The handle is an opaque i32 where 0 is a
// valid value, so we need a separate init flag to detect "not
// yet fetched". Lives at 72..79, the reserved-for-future window
// between strIdxScratchAddr and closuresBase.
//
// Mirrors the WAT path which keeps the same cache in memory[124]
// + bit 4 of the init-flags byte at memory[112]; the wasmbin
// layout doesn't share the init-flags byte across helpers, so
// the network cache owns its own 4-byte init slot.
const (
	networkHandleInitAddr = 72
	networkHandleAddr     = 76
)

// buildPrintBody assembles the wasm bytes for __lang_print.
//
// Signature: (param $data i32) (param $len i32) (result)
//
// Logical:
//
//	L   = __lang_str_len(data, len)
//	dst = __lang_alloc(L)
//	for i in 0..L: mem[dst+i] = __lang_str_byte(data, len, i)
//	mem[48..52] = dst   ; iov_base
//	mem[52..56] = L     ; iov_len
//	wasi_fd_write(1, 48, 1, 56)
//	drop result
//
// Wasm locals (after the two params):
//
//	2: $L
//	3: $dst
//	4: $i
func buildPrintBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 1, true)
}

// buildEprintBody — (data, len) → (). Same shape as
// buildPrintBody but writes to fd=2 (stderr) instead of fd=1.
// Also appends a trailing newline, matching the WAT path's
// fmt.Println-shaped pairing of print/eprint.
func buildEprintBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 2, true)
}

// buildWriteBody — (data, len) → (). Like buildPrintBody but
// without the trailing newline (fd=1, no `\n` append).
func buildWriteBody(idxs map[string]uint32) []byte {
	return buildPrintBodyFd(idxs, 1, false)
}

// buildPrintBodyFd is the fd-parametrised shared implementation
// of __lang_print / __lang_eprint / __lang_write. Same
// str-to-heap copy + fd_write path; the fd and the optional
// trailing-newline are the only deltas.
func buildPrintBodyFd(idxs map[string]uint32, fd int32, withNewline bool) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	alloc := idxs["__lang_alloc"]
	fdWrite := idxs["wasi_fd_write"]
	var body []byte
	// L = __lang_str_len(data, len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 2) // $L
	// dst = __lang_alloc(L + (1 if withNewline else 0)). The
	// trailing newline byte for print / eprint lives one byte
	// past the copied string content; write() skips it.
	body = inst.InstLocalGet(body, 2)
	if withNewline {
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
	}
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 3) // $dst
	// Copy loop: for i in 0..L: mem[dst+i] = __lang_str_byte(data, len, i).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 4) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1) // exit on $i >= $L
		// mem[dst + i] = __lang_str_byte(data, len, i)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i++
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	if withNewline {
		// mem[dst + L] = '\n'  (the byte we reserved at alloc).
		// store8 takes (addr, value); push them in that order.
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, '\n')
		body = memory.InstI32Store8(body, 0, 0)
		// $L = L + 1 (so the iov_len covers the newline too)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
	}
	// mem[printIovecAddr] = dst (iov_base)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// mem[printIovecAddr + 4] = L (iov_len)
	body = inst.InstI32Const(body, printIovecAddr+4)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	// wasi_fd_write(fd, iovec_addr, 1, ret_addr); drop result.
	body = inst.InstI32Const(body, fd)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, 1) // iovec count
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstDrop(body)
	// Three i32 locals: $L, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildExitBody assembles the wasm bytes for __lang_exit.
//
// Signature: (param $code i32) (result)
//
// Body is a single call to wasi_proc_exit, which never returns.
// `unreachable` at the end satisfies the wasm verifier (every
// function body must structurally end somewhere even when
// execution can't actually reach it).
func buildExitBody(idxs map[string]uint32) []byte {
	procExit := idxs["wasi_proc_exit"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // $code
	body = inst.InstCall(body, procExit)
	body = inst.InstUnreachable(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildRandomI32Body assembles the wasm bytes for __lang_random_i32.
//
// Signature: () → i32
//
// Body calls wasi_random_get(randomBufAddr, 4); ignores the
// returned errno; reads back the 4 bytes as an i32. Uses a
// fixed-address scratch instead of allocating to avoid leaking
// memory when called in a loop (the bump allocator never frees).
func buildRandomI32Body(idxs map[string]uint32) []byte {
	randomGet := idxs["wasi_random_get"]
	var body []byte
	body = inst.InstI32Const(body, randomBufAddr)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, randomGet)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstI32Const(body, randomBufAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildNowNsBody assembles the wasm bytes for __lang_now_ns.
//
// Signature: () → i64
//
// Body:
//
//	buf = __lang_alloc(8)
//	wasi_clock_time_get(0 /* realtime */, 0 /* precision */, buf)
//	drop errno
//	return i64.load(buf)
//
// Allocates per call so the 8-byte target buffer doesn't clash
// with any other fixed-address scratch.
func buildNowNsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 0, false)
}

// buildMonotonicNsBody — () → i64. Same as __lang_now_ns but
// uses CLOCK_MONOTONIC (clock_id=1) so the reading is
// monotonically non-decreasing across NTP adjustments.
func buildMonotonicNsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 1, false)
}

// buildNowUnixMsBody — () → i64. Calls wasi_clock_time_get
// (CLOCK_REALTIME) and divides by 1_000_000 to get milliseconds.
func buildNowUnixMsBody(idxs map[string]uint32) []byte {
	return buildClockBody(idxs, 0, true)
}

// buildClockBody is the shared core: alloc 8 bytes, call
// wasi_clock_time_get(clockID, 0, buf), load i64, optionally
// divide by 1_000_000 for the ms variant.
func buildClockBody(idxs map[string]uint32, clockID int32, divideMs bool) []byte {
	alloc := idxs["__lang_alloc"]
	clockTime := idxs["wasi_clock_time_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstI32Const(body, clockID)
	body = inst.InstI64Const(body, 0) // precision = 0
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, clockTime)
	body = inst.InstDrop(body) // ignore errno
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	if divideMs {
		body = inst.InstI64Const(body, 1_000_000)
		body = numeric.InstI64DivS(body)
	}
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvCountBody assembles the wasm bytes for __lang_env_count.
//
// Signature: () → i32 (envc)
//
// Body:
//
//	buf = __lang_alloc(8)               ; two i32 output slots
//	wasi_environ_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; envc lives at +0
func buildEnvCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	envSizes := idxs["wasi_environ_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, envSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgCountBody assembles the wasm bytes for __lang_arg_count.
//
// Signature: () → i32 (argc)
//
// Body:
//
//	buf = __lang_alloc(8)               ; two i32 output slots
//	wasi_args_sizes_get(buf, buf + 4)
//	drop errno
//	return i32.load(buf)                ; argc lives at +0
func buildArgCountBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	var body []byte
	body = inst.InstI32Const(body, 8)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0) // $buf
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, argsSizes)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgAtBody assembles __lang_arg_at.
//
// Signature: (param $i i32) (result i32 i32) — (data, len) pair.
//
// Logic: lazily call wasi_args_sizes_get + wasi_args_get on first
// call, caching (count, argv_ptrs) in low memory. Each call walks
// argv_ptrs[i] until the NUL byte to recover the length, then
// returns (cstr, len) as a heap-form string (top bit of len = 0).
//
// Out-of-range i (signed-negative or i >= argc) returns (0, 0).
//
// Locals (after the one param):
//
//	1: $argc
//	2: $bufsize
//	3: $argv_ptrs
//	4: $argv_buf
//	5: $cstr
//	6: $len
func buildArgAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	argsSizes := idxs["wasi_args_sizes_get"]
	argsGet := idxs["wasi_args_get"]
	var body []byte
	body = inst.InstI32Const(body, argsInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = inst.InstCall(body, argsSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1) // $argc
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2) // $bufsize
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3) // $argv_ptrs
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4) // $argv_buf
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, argsGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// Bounds check via unsigned compare: rejects negatives + overshoot.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// cstr = mem[args_ptrs + i*4]
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5) // $cstr
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $len = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvAtBody — mirror of buildArgAtBody, routed through
// wasi_environ_sizes_get + wasi_environ_get. Each returned
// (data, len) covers a full "KEY=VALUE" entry; user code splits
// on '=' if needed.
func buildEnvAtBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	envSizes := idxs["wasi_environ_sizes_get"]
	envGet := idxs["wasi_environ_get"]
	var body []byte
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = inst.InstCall(body, envSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstCall(body, envGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, envCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstI32Const(body, envPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
		body = numeric.InstI32Eqz(body)
		body = inst.InstBrIf(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadByteBody assembles __lang_read_byte.
//
// Signature: () → i32 — returns 0..255 for a byte read from
// stdin, or -1 for EOF / error.
//
// First call alloc(16) for the scratch region and caches the
// base addr in low memory (readByteScratchAddr). Layout within
// the scratch region: iov.base (4), iov.len (4), nread out (4),
// 1-byte buffer (rounded up to 4 = 4 bytes). Total 16 bytes.
//
//	scratch + 0..3:   iov.base (set per call to scratch+12)
//	scratch + 4..7:   iov.len = 1
//	scratch + 8..11:  nread output
//	scratch + 12..15: byte buffer (only [12] used)
//
// Each call: write iov struct + invoke fd_read(0, scratch, 1,
// scratch+8); on errno != 0 or nread == 0 → -1, otherwise load
// the byte at scratch+12.
//
// Locals (no params):
//
//	0: $scratch
//	1: $errno
func buildReadByteBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	fdRead := idxs["wasi_fd_read"]
	var body []byte
	// $scratch = mem[readByteScratchAddr]
	body = inst.InstI32Const(body, readByteScratchAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 0)
	// If zero, alloc(16) and store the pointer back.
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 16)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 0)
		body = inst.InstI32Const(body, readByteScratchAddr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// iov.base = scratch + 12 (the byte buffer slot)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	// iov.len = 1
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// fd_read(0 /* stdin */, scratch, 1, scratch+8)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, fdRead)
	body = inst.InstLocalSet(body, 1) // $errno
	// If errno != 0, return -1.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// If nread == 0 (EOF), return -1.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, -1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Read byte at scratch+12.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Load8U(body, 0, 0)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildPutcharBody — (b) → (). Writes the low byte of b to
// stdout via wasi_fd_write. Uses the print iovec scratch
// region (printIovecAddr=48..55) as a 1-byte buffer at
// printIovecAddr+0 with iovec base=printIovecAddr+0 (the
// next 4 bytes hold the byte) and iov_len=1; the iovec
// descriptor reuses printIovecAddr/+4. Reads back the same
// nwritten slot at printRetAddr.
//
// Memory layout per call:
//
//	mem[48..51]: iov_base = 52
//	mem[52]:     the byte (one byte; we use a 4-byte slot for alignment)
//	mem[56..59]: iov_len = 1 (overlaps printRetAddr — we overwrite
//	             after fd_write reads it)
//
// To avoid the iov_base/iov_len-overlap-with-the-byte-slot issue,
// shift the byte buffer to a dedicated slot inside the existing
// scratch region. We write the byte at printRetAddr (since the
// fd_write result is dropped anyway), iov_base=printRetAddr, iov_len=1,
// stored at printIovecAddr/+4.
func buildPutcharBody(idxs map[string]uint32) []byte {
	fdWrite := idxs["wasi_fd_write"]
	var body []byte
	// mem[printRetAddr] = b (low byte; we only read one)
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store8(body, 0, 0)
	// mem[printIovecAddr] = printRetAddr (iov_base)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, printRetAddr)
	body = memory.InstI32Store(body, 2, 0)
	// mem[printIovecAddr + 4] = 1 (iov_len)
	body = inst.InstI32Const(body, printIovecAddr+4)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// wasi_fd_write(1, iovec_addr, 1, ret_addr); drop result.
	// Note: ret_addr overlaps the byte buffer; that's fine since
	// we never read the byte back, and fd_write writes nwritten
	// after reading iov_base/iov_len.
	body = inst.InstI32Const(body, 1)
	body = inst.InstI32Const(body, printIovecAddr)
	body = inst.InstI32Const(body, 1)
	body = inst.InstI32Const(body, printRetAddr)
	body = inst.InstCall(body, fdWrite)
	body = inst.InstDrop(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildRandomBytesBody — (n) → (data, len). Allocates an
// n-byte heap buffer and fills it with cryptographic-quality
// random bytes via wasi_random_get. Returns the (data, len)
// pair in heap form (top bit of len clear).
//
//	n == 0:  return inline empty (0, 0x80000000).
//	n > 0:   data = alloc(n); random_get(data, n); return (data, n).
//
// Locals (after the one param):
//
//	1: $buf
func buildRandomBytesBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	randomGet := idxs["wasi_random_get"]
	var body []byte
	// if n == 0: return inline empty (0, 0x80000000)
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// $buf = __lang_alloc(n)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1)
	// wasi_random_get($buf, n); drop errno.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, randomGet)
	body = inst.InstDrop(body)
	// Return ($buf, n)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildEnvBody — (name_data, name_len) → i32 (Option[string]
// box). Looks up an env variable by name in the cached
// environ_ptrs. Returns Some(value) on match, None otherwise.
//
// Layout matches __lang_read_line's Option[string] box:
//   Some(line): 16-byte alloc, tag=0 at +0, data at +8, len at +12.
//   None:       4-byte alloc, tag=1 at +0.
//
// Algorithm:
//   - Lazily init the env cache (shared with __lang_env_at).
//   - For each i in 0..envc:
//     - entry = environ_ptrs[i]  (NUL-terminated "KEY=VALUE")
//     - Walk j from 0:
//       - byte = mem[entry + j]
//       - If j == name_len AND byte == '=': match found
//       - If j == name_len OR byte != name[j]: no match, next i
//     - When match: value_start = entry + j + 1
//       value_len = strlen(value_start)
//       Build Some(value).
//   - If no entry matches: return None.
//
// Locals (after 2 params):
//
//	2: $i        — outer entry index
//	3: $entry    — current environ_ptrs[i]
//	4: $j        — byte offset within entry
//	5: $entry_b  — byte at entry+j
//	6: $name_b   — byte at name_data+j (looked up via __lang_str_byte)
//	7: $value    — value-start pointer (entry + j + 1) on match
//	8: $vlen     — value length (strlen)
//	9: $box      — Option box pointer for return
//	10: $name_real_len — strlen of name (via __lang_str_len)
func buildEnvBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	envSizes := idxs["wasi_environ_sizes_get"]
	envGet := idxs["wasi_environ_get"]
	var body []byte
	// Lazy init env cache (same shape as __lang_env_at).
	body = inst.InstI32Const(body, envInitAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = inst.InstCall(body, envSizes)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envSizesArgcAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2) // reuse $i slot temporarily for envc
		body = inst.InstI32Const(body, envSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 3) // reuse $entry slot for bufsize
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 4) // env_ptrs (reuse $j)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 5) // env_buf (reuse $entry_b)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstCall(body, envGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, envCountAddr)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envPtrsAddr)
		body = inst.InstLocalGet(body, 4)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, envInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// $name_real_len = __lang_str_len(name_data, name_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 10)
	// for i in 0..envc:
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 2)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // outer
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= envc: break (no match)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, envCountAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $entry = mem[env_ptrs + i*4]
		body = inst.InstI32Const(body, envPtrsAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 3)
		// Compare prefix of entry with name. $j = 0.
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 4)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty) // cmp block
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// $entry_b = mem[entry + j]
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load8U(body, 0, 0)
			body = inst.InstLocalSet(body, 5)
			// If $j == name_real_len: check entry_b == '='
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 10)
			body = numeric.InstI32Eq(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				// entry_b == '=' (61)?
				body = inst.InstLocalGet(body, 5)
				body = inst.InstI32Const(body, 61)
				body = numeric.InstI32Eq(body)
				body = inst.InstIfStart(body, inst.BlocktypeEmpty)
				{
					// Match: build Some(value). value starts at
					// entry + j + 1. strlen via NUL scan.
					body = inst.InstLocalGet(body, 3)
					body = inst.InstLocalGet(body, 4)
					body = numeric.InstI32Add(body)
					body = inst.InstI32Const(body, 1)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalSet(body, 7) // $value
					// $vlen = 0; while mem[value + vlen] != 0: vlen++
					body = inst.InstI32Const(body, 0)
					body = inst.InstLocalSet(body, 8)
					body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
					body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
					{
						body = inst.InstLocalGet(body, 7)
						body = inst.InstLocalGet(body, 8)
						body = numeric.InstI32Add(body)
						body = memory.InstI32Load8U(body, 0, 0)
						body = numeric.InstI32Eqz(body)
						body = inst.InstBrIf(body, 1)
						body = inst.InstLocalGet(body, 8)
						body = inst.InstI32Const(body, 1)
						body = numeric.InstI32Add(body)
						body = inst.InstLocalSet(body, 8)
						body = inst.InstBr(body, 0)
					}
					body = inst.InstEnd(body)
					body = inst.InstEnd(body)
					// Build Some box: alloc(16), tag=0, data, len.
					body = inst.InstI32Const(body, 16)
					body = inst.InstCall(body, alloc)
					body = inst.InstLocalSet(body, 9)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 0)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 8)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalGet(body, 7)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstI32Const(body, 12)
					body = numeric.InstI32Add(body)
					body = inst.InstLocalGet(body, 8)
					body = memory.InstI32Store(body, 2, 0)
					body = inst.InstLocalGet(body, 9)
					body = inst.InstReturn(body)
				}
				body = inst.InstEnd(body)
				// Mismatch (name fully consumed but no '=' yet).
				// Exit cmp loop, continue outer.
				body = inst.InstBr(body, 2) // break out of inner block (cmp)
			}
			body = inst.InstEnd(body)
			// If entry_b == 0 (premature NUL) or entry_b != name[j]:
			// not a match.
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32Eqz(body)
			body = inst.InstBrIf(body, 1) // break cmp loop
			// $name_b = __lang_str_byte(name_data, name_len, j)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstCall(body, strByte)
			body = inst.InstLocalSet(body, 6)
			// If entry_b != name_b: break cmp loop
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 6)
			body = numeric.InstI32Ne(body)
			body = inst.InstBrIf(body, 1) // break cmp loop
			// $j++
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 4)
			body = inst.InstBr(body, 0) // continue cmp loop
		}
		body = inst.InstEnd(body) // end cmp loop
		body = inst.InstEnd(body) // end cmp block
		// Next entry.
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end outer loop
	body = inst.InstEnd(body) // end outer block
	// No match: return None. alloc(4), tag=1.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 9)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 9)
	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}
