// Synthetic runtime-helper functions appended to the module after
// the user functions. These exist to implement IR ops (OpAlloc,
// OpStrLen, OpStrEq, OpStrConcat, OpStrLen-byte, the __lang_print
// WASI wrapper, etc.) without forcing every caller to inline the
// same code sequence.
//
// Each helper is gated by a usage scan over prog.Funcs — programs
// that never need a helper pay zero bytes for its body.
// runtimeHelperSpecs keeps the names + bodies + signatures in one
// place so adding a new helper is one entry.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// memInst* short aliases keep the buildAllocBody assembly readable
// without each line repeating the package qualifier. Alignment 2
// (log2 of 4 bytes) for i32 ops; offset 0 means the cursor address
// is the literal i32 on top of the stack at call time.
func memInstI32Load(buf []byte) []byte    { return memory.InstI32Load(buf, 2, 0) }
func memInstI32Store(buf []byte) []byte   { return memory.InstI32Store(buf, 2, 0) }
func memInstMemorySize(buf []byte) []byte { return memory.InstMemorySize(buf) }
func memInstMemoryGrow(buf []byte) []byte { return memory.InstMemoryGrow(buf) }

// runtimeHelperSpec describes one helper function's signature +
// pre-built body. Bodies are produced lazily (the `body` closure)
// so the heavy hand-crafted byte sequences only run when the
// helper is actually used.
//
// Bodies that call sibling helpers (e.g. __str_eq → __lang_str_len
// + __lang_str_byte) receive a name → funcidx map so the call
// targets are resolved at module-assembly time without any
// post-emission patching.
type runtimeHelperSpec struct {
	params  []byte
	results []byte
	body    func(helperIdxs map[string]uint32) []byte
}

// runtimeNeeds is the set of helpers a single Emit call needs,
// in stable order so the funcidx assignment is deterministic.
type runtimeNeeds struct {
	order []string // names in declaration order
	set   map[string]bool
}

func (r *runtimeNeeds) add(name string) {
	if r.set == nil {
		r.set = map[string]bool{}
	}
	if r.set[name] {
		return
	}
	r.set[name] = true
	r.order = append(r.order, name)
}

// scanRuntimeHelpers walks the IR program and records every
// helper its ops will need. Each entry here corresponds to one
// helper in runtimeHelperSpecs.
func scanRuntimeHelpers(prog *ir.Program) runtimeNeeds {
	var needs runtimeNeeds
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpStrLen:
				needs.add("__lang_str_len")
			case ir.OpAlloc:
				needs.add("__lang_alloc")
			case ir.OpMakeClosure, ir.OpMakeEnv:
				// Both ops call __lang_alloc to materialise
				// the env block; OpMakeClosure also allocs
				// a second 8-byte pair cell.
				needs.add("__lang_alloc")
			case ir.OpCallDirect:
				// Source-language built-ins lower to OpCallDirect
				// with the source name; the call-site lookup
				// goes through callDirectAlias which routes to
				// the synthetic helper. The trigger here uses
				// the same alias so the helper actually exists.
				switch callDirectAlias(op.Str) {
				case "__lang_print":
					// fd_write under the hood; transitively
					// pulls in the byte-copy + alloc helpers.
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_alloc")
					needs.add("__lang_print")
				case "__lang_eprint":
					// Same shape as __lang_print but fd=2 (stderr).
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_alloc")
					needs.add("__lang_eprint")
				case "__lang_write":
					// Same shape as __lang_print but no trailing
					// newline (fd=1).
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_alloc")
					needs.add("__lang_write")
				case "__lang_putchar":
					// (b) → () — single-byte fd_write to stdout.
					needs.add("__lang_putchar")
				case "__lang_exit":
					// wasi_proc_exit under the hood; nothing
					// else needed.
					needs.add("__lang_exit")
				case "__lang_random_i32":
					// wasi_random_get under the hood; writes
					// 4 random bytes to the fixed scratch slot
					// and returns them as an i32.
					needs.add("__lang_random_i32")
				case "__lang_random_bytes":
					// (n) → (data, len) — wasi_random_get into
					// a fresh n-byte heap allocation. Returns
					// the (data, len) pair of the heap string.
					needs.add("__lang_alloc")
					needs.add("__lang_random_bytes")
				case "__lang_now_ns":
					// wasi_clock_time_get + alloc-per-call for
					// the 8-byte output buffer.
					needs.add("__lang_alloc")
					needs.add("__lang_now_ns")
				case "__lang_now_unix_ms":
					// Same as __lang_now_ns / 1_000_000.
					needs.add("__lang_alloc")
					needs.add("__lang_now_unix_ms")
				case "__lang_monotonic_ns":
					// CLOCK_MONOTONIC (1) variant of __lang_now_ns.
					needs.add("__lang_alloc")
					needs.add("__lang_monotonic_ns")
				case "__lang_arena_save":
					// Reads the bump-allocator cursor at mem[40].
					// No alloc dependency; the cursor lives in
					// reserved low memory regardless.
					needs.add("__lang_arena_save")
				case "__lang_arena_restore":
					// Writes mem[40] = handle.
					needs.add("__lang_arena_restore")
				case "__lang_sqrt_f64":
					needs.add("__lang_sqrt_f64")
				case "__lang_abs_f64":
					needs.add("__lang_abs_f64")
				case "__lang_floor_f64":
					needs.add("__lang_floor_f64")
				case "__lang_ceil_f64":
					needs.add("__lang_ceil_f64")
				case "__lang_trunc_f64":
					needs.add("__lang_trunc_f64")
				case "__lang_env_count":
					// wasi_environ_sizes_get + alloc-per-call
					// for the 8-byte output buffer.
					needs.add("__lang_alloc")
					needs.add("__lang_env_count")
				case "__lang_arg_count":
					// wasi_args_sizes_get + alloc-per-call
					// for the 8-byte output buffer.
					needs.add("__lang_alloc")
					needs.add("__lang_arg_count")
				case "__lang_arg_at":
					// wasi_args_sizes_get + wasi_args_get +
					// alloc for the argv_ptrs table + argv buf.
					// One-shot init cached in low memory.
					needs.add("__lang_alloc")
					needs.add("__lang_arg_at")
				case "__lang_args":
					// Builds a string[] of all argv entries.
					// Shares the wasi_args_* init path with
					// __lang_arg_at via the low-memory cache.
					needs.add("__lang_alloc")
					needs.add("__lang_args")
				case "__lang_env_at":
					// wasi_environ_sizes_get + wasi_environ_get
					// + alloc for the environ_ptrs table + buf.
					needs.add("__lang_alloc")
					needs.add("__lang_env_at")
				case "__lang_env":
					// (name) → Option[string]. Walks the cached
					// environ_ptrs comparing each entry's prefix
					// up to '=' against name.
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_env")
				case "__lang_read_byte":
					// wasi_fd_read on stdin (fd=0) + alloc for
					// the per-process scratch region.
					needs.add("__lang_alloc")
					needs.add("__lang_read_byte")
				case "__lang_read_line":
					// Reads bytes via __lang_read_byte until '\n'
					// or EOF, accumulates into a growable buffer,
					// then builds an Option[string] heap box.
					needs.add("__lang_alloc")
					needs.add("__lang_read_byte")
					needs.add("__lang_read_line")
				case "__lang_stdin":
					// () → i32 — Reader struct with fd=0 (stdin).
					// Backs `stdin()`; the `__method_Reader_*`
					// helpers dispatch on r.fd so the same code
					// path covers stdin and file Readers.
					needs.add("__lang_alloc")
					needs.add("__lang_stdin")
				case "__lang_reader_read_line_fd":
					// (r) → i32 — heap-form Option[string]. Reads
					// from r.fd byte-by-byte until '\n' / EOF.
					needs.add("__lang_alloc")
					needs.add("__lang_reader_read_line_fd")
				case "__lang_reader_read_chunk":
					// (r, n) → i32 — single fd_read of up to n
					// bytes into a fresh n-byte heap buffer.
					needs.add("__lang_alloc")
					needs.add("__lang_reader_read_chunk")
				case "__lang_reader_close_fd":
					// (r) → i32 — fd_close on r.fd; returns
					// Option[IoError].
					needs.add("__lang_alloc")
					needs.add("__build_io_error")
					needs.add("__lang_reader_close_fd")
				case "__lang_writer_close":
					// Same shape as reader_close — Writer struct
					// has identical { fd: i32 } layout.
					needs.add("__lang_alloc")
					needs.add("__build_io_error")
					needs.add("__lang_writer_close")
				case "__lang_writer_write":
					// (w, s_data, s_len) → i32 — fd_write loop
					// over the SSO-normalized content bytes.
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_writer_write")
				case "__lang_open_reader":
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_open_reader")
				case "__lang_open_writer":
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_open_writer")
				case "__lang_open_appender":
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_open_appender")
				case "__lang_string_from_bytes":
					// (bs: u8[]) → (data, len) — copies the byte
					// array's payload into a fresh string. Inline
					// fast-path for len ≤ 7, heap copy otherwise.
					needs.add("__lang_alloc")
					needs.add("__lang_string_from_bytes")
				case "__lang_read_file":
					// (path) → Result[string, IoError]. Pulls in
					// __build_io_error for the error-path variant
					// construction; __lang_str_len / __lang_str_byte
					// are needed to SSO-normalize the path argument
					// before it reaches path_open. WASI imports
					// (path_open / fd_read / fd_close) get added by
					// scanImports below once this helper is in the
					// needs set.
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_read_file")
				case "__lang_write_file":
					// (path, content) → Option[IoError]. Same
					// __build_io_error / __lang_str_len /
					// __lang_str_byte chain as read_file plus
					// the str-normalize loop reusing them; the
					// scanRuntimeHelpers transitive close still
					// pulls them in via `needs.add` here.
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__build_io_error")
					needs.add("__lang_write_file")
				case "__lang_tcp_listen":
					// (port) → i32 — heap pointer to a 12-byte
					// listener struct (sock, 0, 0), or -errno
					// on failure. Pulls in the __network_handle
					// accessor that caches wasi:sockets/instance-
					// network. WASI imports get added by
					// scanImports below.
					needs.add("__lang_alloc")
					needs.add("__network_handle")
					needs.add("__lang_tcp_listen")
				case "__lang_tcp_accept":
					// (listener) → i32 — heap pointer to a
					// 12-byte connection struct (sock, instream,
					// outstream), or -errno on failure.
					needs.add("__lang_alloc")
					needs.add("__lang_tcp_accept")
				case "__lang_tcp_recv":
					// (conn, max) → (data, len) — heap-form
					// string with the bytes read. Empty on
					// stream-error / EOF.
					needs.add("__lang_alloc")
					needs.add("__lang_tcp_recv")
				case "__lang_tcp_send":
					// (conn, data) → i32 — bytes sent, -1 on
					// failure. SSO-normalizes the input string
					// so inline-form data flows through the
					// host's read of (ptr, len).
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_tcp_send")
				case "__lang_tcp_close":
					// (conn) → i32 (always 0). Drops the
					// streams (if non-zero) before the parent
					// tcp-socket to satisfy the canonical-ABI
					// resource-has-children rule.
					needs.add("__lang_tcp_close")
				case "__slice_make":
					needs.add("__lang_alloc")
					needs.add("__slice_make")
				case "__slice_idx":
					needs.add("__slice_idx")
				case "__slice_idx_1":
					needs.add("__slice_idx_1")
				case "__slice_idx_2":
					needs.add("__slice_idx_2")
				case "__slice_idx_4":
					needs.add("__slice_idx_4")
				case "__slice_idx_8":
					needs.add("__slice_idx_8")
				case "__method_string_as_bytes":
					needs.add("__lang_alloc")
					needs.add("__lang_str_len")
					needs.add("__method_string_as_bytes")
				case "__lang_stdout":
					needs.add("__lang_alloc")
					needs.add("__lang_stdout")
				case "__lang_stderr":
					needs.add("__lang_alloc")
					needs.add("__lang_stderr")
				}
				// Low-level memory shims the stdlib calls directly
				// (raw OpCallDirect, no callDirectAlias rewrite).
				// Each is a one-instruction wrapper around a wasm
				// load / store / bulk-memory op so stdlib `.lang`
				// code can drop into raw memory without leaving the
				// language.
				switch op.Str {
				case "__load_i32":
					needs.add("__load_i32")
				case "__store_i32":
					needs.add("__store_i32")
				case "__load_i64":
					needs.add("__load_i64")
				case "__store_i64":
					needs.add("__store_i64")
				case "__load_ptr":
					needs.add("__load_ptr")
				case "__store_ptr":
					needs.add("__store_ptr")
				case "__ptr_width":
					needs.add("__ptr_width")
				case "__alloc", "__alloc_u8":
					needs.add("__lang_alloc")
					needs.add(op.Str)
				case "__memcpy":
					needs.add("__memcpy")
				case "__memset":
					needs.add("__memset")
				case "__lang_rc_inc":
					needs.add("__lang_rc_inc")
				case "__lang_rc_dec":
					needs.add("__lang_rc_dec")
				case "__lang_arr_push_grow":
					needs.add("__lang_arr_push_grow")
					needs.add("__lang_alloc")
					needs.add("__memcpy")
				case "__str_idx":
					// Same byte-fetch SSO seam used by
					// __lang_str_byte but returns a byte
					// address that the caller's OpLoadByte
					// dereferences. Used in __map_hash's
					// string-key path.
					needs.add("__lang_str_len")
					needs.add("__str_idx")
				case "__arr_idx":
					// (base, i) → byte address of element i
					// in a 4-byte-stride array. Length prefix
					// at [base-4].
					needs.add("__arr_idx")
				case "__arr_idx_1":
					// Stride-1 byte-array indexing.
					needs.add("__arr_idx_1")
				case "__arr_idx_2":
					// Stride-2 halfword-array indexing.
					needs.add("__arr_idx_2")
				case "__arr_idx_8":
					// Stride-8 i64 / f64 array indexing.
					needs.add("__arr_idx_8")
				case "__str_slice":
					// (base_data, base_len, low, high) → (data, len)
					// — copy bytes [low..high] into a fresh string.
					needs.add("__lang_str_len")
					needs.add("__lang_str_byte")
					needs.add("__lang_alloc")
					needs.add("__str_slice")
				}
			case ir.OpStrEq:
				// __str_eq's inline-side byte reads route
				// through __lang_str_byte, and the length
				// dispatch uses __lang_str_len.
				needs.add("__lang_str_len")
				needs.add("__lang_str_byte")
				needs.add("__str_eq")
			case ir.OpStrConcat:
				// __str_concat allocates a buffer sized by
				// the sum of the two operand lengths, then
				// copies bytes one-at-a-time via the SSO-
				// aware byte fetch. Returns the new (data,
				// len) pair as a heap-form string.
				needs.add("__lang_str_len")
				needs.add("__lang_str_byte")
				needs.add("__lang_alloc")
				needs.add("__str_concat")
			}
		}
	}
	return needs
}

// runtimeHelperSpecs is the registry. Keyed by the canonical
// helper name; the entry's body() builds the wasm bytes lazily.
var runtimeHelperSpecs = map[string]runtimeHelperSpec{
	"__lang_str_len": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32}, // (data, len)
		results: []byte{encode.ValtypeI32},
		body:    buildStrLenBody,
	},
	"__lang_alloc": {
		params:  []byte{encode.ValtypeI32}, // size
		results: []byte{encode.ValtypeI32}, // pointer
		body:    buildAllocBody,
	},
	"__lang_str_byte": {
		// (data, len, i) → i32 byte; inline-or-heap aware.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrByteBody,
	},
	"__lang_print": {
		// (data, len) → ()
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildPrintBody,
	},
	"__lang_eprint": {
		// (data, len) → () — same shape as __lang_print but
		// writes to fd=2 (stderr) instead of fd=1.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildEprintBody,
	},
	"__lang_write": {
		// (data, len) → () — like __lang_print but without
		// the trailing newline. The pair `print` / `write`
		// mirrors Go's fmt.Println / fmt.Print.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildWriteBody,
	},
	"__lang_putchar": {
		// (b) → () — fd_write a single byte to stdout. Uses
		// the print iovec scratch region as a 1-byte buffer.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildPutcharBody,
	},
	"__lang_exit": {
		// (code) → () — never returns, but the wasm signature
		// still has a void result.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildExitBody,
	},
	"__lang_random_i32": {
		// () → i32 — host-supplied random word via wasi_random_get.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildRandomI32Body,
	},
	"__lang_random_bytes": {
		// (n) → (data, len) — heap-form string of n random
		// bytes via wasi_random_get. Empty (n=0) → inline empty
		// (0, 0x80000000).
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildRandomBytesBody,
	},
	"__lang_now_ns": {
		// () → i64 — nanoseconds since unix epoch from the
		// realtime clock via wasi_clock_time_get.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildNowNsBody,
	},
	"__lang_now_unix_ms": {
		// () → i64 — milliseconds since unix epoch. Calls
		// wasi_clock_time_get (CLOCK_REALTIME) and divides by
		// 1_000_000.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildNowUnixMsBody,
	},
	"__lang_monotonic_ns": {
		// () → i64 — monotonic nanoseconds via
		// wasi_clock_time_get (CLOCK_MONOTONIC = 1).
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildMonotonicNsBody,
	},
	"__lang_arena_save": {
		// () → i32 — snapshot of the bump-allocator cursor.
		// Pair with __lang_arena_restore to free everything
		// allocated since the save in one pointer-store.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArenaSaveBody,
	},
	"__lang_arena_restore": {
		// (handle) → () — rewinds the bump cursor to handle.
		// Pointers into the freed region are no longer valid;
		// caller discipline enforces non-use.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildArenaRestoreBody,
	},
	"__lang_sqrt_f64": {
		// (f64) → f64 — wasm-native f64.sqrt.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildSqrtF64Body,
	},
	"__lang_abs_f64": {
		// (f64) → f64 — wasm-native f64.abs.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildAbsF64Body,
	},
	"__lang_floor_f64": {
		// (f64) → f64 — wasm-native f64.floor.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildFloorF64Body,
	},
	"__lang_ceil_f64": {
		// (f64) → f64 — wasm-native f64.ceil.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildCeilF64Body,
	},
	"__lang_trunc_f64": {
		// (f64) → f64 — wasm-native f64.trunc.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildTruncF64Body,
	},
	"__lang_env_count": {
		// () → i32 — count of environment variables (envc).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildEnvCountBody,
	},
	"__lang_arg_count": {
		// () → i32 — count of command-line args (argc).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArgCountBody,
	},
	"__lang_arg_at": {
		// (i) → (data, len) — the i-th argv string. (0, 0)
		// for i out of [0..argc). Lazily inits + caches argv
		// in low memory on first call.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildArgAtBody,
	},
	"__lang_args": {
		// () → i32 — length-prefixed string[] of all argv
		// entries. Returns the data pointer (length lives at
		// data - 4). Each entry is a 2-word (data, len) pair
		// in heap form (top bit of len clear). Cached after
		// first build.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArgsBody,
	},
	"__lang_env_at": {
		// (i) → (data, len) — the i-th environ entry as a
		// "KEY=VALUE" string. (0, 0) for i out of range.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildEnvAtBody,
	},
	"__lang_env": {
		// (name_data, name_len) → Option[string] heap box.
		// Walks the cached environ_ptrs comparing each
		// entry's prefix up to '=' with name. Returns
		// Some(value) on match, None otherwise.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildEnvBody,
	},
	"__lang_read_byte": {
		// () → i32 — one byte from stdin (0..255), or -1 on
		// EOF/error. Lazily alloc()s a 16-byte scratch region
		// for the iovec + nread out + the 1-byte read buffer.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildReadByteBody,
	},
	"__lang_read_line": {
		// () → i32 — heap pointer to an Option[string] box.
		// Some(line) box layout (16 bytes): tag=0 at +0,
		// data ptr at +8, len at +12 (Option[string] payload
		// is aligned to 8). None box layout (4 bytes): tag=1
		// at +0. Line includes the trailing '\n' if present;
		// returns None on EOF before any byte was read.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildReadLineBody,
	},
	"__lang_stdin": {
		// () → i32 — constant sentinel Reader. wasmbin doesn't
		// yet model TCP / file Readers (no `tcp_listen` / file
		// preopens), so the value is opaque and only the
		// stdin-specific Reader methods consume it.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStdinBody,
	},
	"__lang_reader_read_line": {
		// (r) → i32 — Reader.read_line(). For wasmbin's stdin-
		// only Reader model, ignores the receiver and delegates
		// to __lang_read_line.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderReadLineBody,
	},
	"__lang_reader_close": {
		// (r) → () — no-op. Drops the receiver. Real Reader.close
		// (file fds, TCP sockets) will need a discriminator-
		// aware path once those Readers exist.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildReaderCloseBody,
	},
	"__lang_string_from_bytes": {
		// (bs) → (data, len) — copies bs's payload into a
		// fresh string. Empty array → inline empty; ≤7 bytes →
		// inline-packed; >7 bytes → heap copy via memory.copy.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStringFromBytesBody,
	},
	"__load_i32": {
		// (addr) → i32 — i32.load wrapper. Stdlib uses this where
		// the language doesn't expose raw memory ops directly.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildLoadI32Body,
	},
	"__store_i32": {
		// (addr, v) → () — i32.store wrapper.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildStoreI32Body,
	},
	"__load_i64": {
		// (addr) → i64 — i64.load wrapper. Map runtime uses this
		// to dereference boxed wide-scalar keys.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI64},
		body:    buildLoadI64Body,
	},
	"__store_i64": {
		// (addr, v) → () — i64.store wrapper.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI64},
		results: nil,
		body:    buildStoreI64Body,
	},
	"__load_ptr": {
		// (addr) → i32 — same as __load_i32 on wasm32 (heap
		// pointer = i32). Lives in the registry under its own
		// name for parity with the stdlib alias.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildLoadI32Body,
	},
	"__store_ptr": {
		// (addr, v) → () — same as __store_i32 on wasm32.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildStoreI32Body,
	},
	"__ptr_width": {
		// () → i32 — 4 on wasm32. Stdlib uses this in size
		// computations that vary between 4-byte and 8-byte
		// targets (the same .lang code runs on arm64).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildPtrWidthBody,
	},
	"__alloc": {
		// (size) → i32 — same as __lang_alloc. Lives in the
		// registry for stdlib parity.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildAliasAllocBody,
	},
	"__lang_rc_inc": {
		// (ptr) → ptr — refcount inc helper. Returns the input
		// pointer so IR codegen can splice an inc into an
		// expression evaluation chain. NULL-safe and
		// sentinel-aware (high bit of rc word = "static, never
		// touch"). See buildRcIncBody +
		// docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildRcIncBody,
	},
	"__lang_rc_dec": {
		// (ptr) → ptr — refcount dec helper. NULL-safe and
		// sentinel-aware. Returns the input ptr so the
		// calling convention matches arm64 / x86_64 (both
		// preserve x0 / rax through the helper). IR-level
		// dec emission can rely on a uniform "OpCallDirect
		// always pushes one result" assumption across every
		// backend. Phase-1 simplification: doesn't free on
		// rc == 1 (the bump allocator leaks). See
		// buildRcDecBody + docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildRcDecBody,
	},
	"__lang_arr_push_grow": {
		// (arr, oldLen, stride) → new_data. Phase 2 mutate-or-
		// copy helper for `arr.push(v)`. Same contract as the
		// arm64 / x86_64 helpers: on rc==1 and oldLen<cap,
		// mutate in place (bump rc to 2 + write len), return
		// arr. Else alloc fresh buffer with cap=max(2*newLen,4),
		// memcpy old data, return new data pointer. See
		// buildArrPushGrowBody + docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushGrowBody,
	},
	"__alloc_u8": {
		// (n) → i32 — allocates a length-prefixed u8[] of
		// length n. Layout: 4-byte i32 length prefix at
		// [base - 4], then n bytes of payload starting at
		// base. Returns the data pointer (base). The bump
		// allocator zeroes fresh pages, so the payload starts
		// out zero.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildAllocU8Body,
	},
	"__memcpy": {
		// (dst, src, n) → () — wasm memory.copy.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildMemcpyBody,
	},
	"__memset": {
		// (dst, b, n) → () — wasm memory.fill. b is treated as
		// a byte (low 8 bits of the i32).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildMemsetBody,
	},
	"__build_io_error": {
		// (errno, path_data, path_len) → i32 — translates a
		// WASI preview-1 errno into a heap-form IoError
		// variant; the address goes into Result.Err's payload
		// slot. See wasi_fs.go for the errno-to-variant map.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildBuildIoErrorBody,
	},
	"__lang_read_file": {
		// (path_data, path_len) → i32 — heap-form
		// Result[string, IoError] pointer. path is interpreted
		// relative to the preopen at fd 3 (the standard
		// `wasmtime --dir=…` mapping). See wasi_fs.go for the
		// streaming-read pipeline.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReadFileBody,
	},
	"__lang_write_file": {
		// (path_data, path_len, content_data, content_len) →
		// i32 — heap-form Option[IoError] pointer (None on
		// success, Some(IoError) on error). Truncates the
		// target via O_CREAT|O_TRUNC; same preopen-fd-3
		// convention as __lang_read_file.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWriteFileBody,
	},
	"__network_handle": {
		// () → i32 — cached wasi:sockets/instance-network handle.
		// Lazily fetched on first call; the init flag at
		// networkHandleInitAddr disambiguates "not yet fetched"
		// from a legitimate 0 handle. See wasi_tcp.go.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildNetworkHandleBody,
	},
	"__lang_tcp_listen": {
		// (port: i32) → i32 — heap pointer to a 12-byte
		// listener struct (tcp-socket, 0, 0) on success;
		// -errno on failure. See wasi_tcp.go.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpListenBody,
	},
	"__lang_tcp_accept": {
		// (listener: i32) → i32 — heap pointer to a 12-byte
		// connection struct (tcp-socket, input-stream,
		// output-stream); -errno on failure.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpAcceptBody,
	},
	"__lang_tcp_recv": {
		// (conn: i32, max: i32) → (data, len) heap-form
		// string. Empty pair (0, 0) on stream-error / EOF.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildTcpRecvBody,
	},
	"__lang_tcp_send": {
		// (conn, data_data, data_len) → i32 — bytes sent on
		// success, -1 on stream-error. Chunked at 4 KiB to
		// match wasmtime's blocking-write-and-flush cap.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpSendBody,
	},
	"__lang_tcp_close": {
		// (conn: i32) → i32 (always 0). Drops streams +
		// tcp-socket in canonical child-before-parent order.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpCloseBody,
	},
	"__lang_open_reader": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Reader, IoError]. The Reader struct holds a
		// preview-1 fd.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenReaderBody,
	},
	"__lang_open_writer": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Writer, IoError]. Opens with CREATE|TRUNCATE.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenWriterBody,
	},
	"__lang_open_appender": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Writer, IoError]. Opens with CREATE + APPEND.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenAppenderBody,
	},
	"__lang_reader_close_fd": {
		// (r: i32) → i32 — heap-form Option[IoError]. Calls
		// fd_close on the Reader's fd; returns None on success.
		// Named `_fd` to distinguish from the existing
		// `__lang_reader_close` which is the stdin-only stub.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderCloseFdBody,
	},
	"__lang_writer_close": {
		// (w: i32) → i32 — same shape as the Reader close, with
		// a dedicated name so the IR alias map can route
		// `__method_Writer_close` here.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWriterCloseBody,
	},
	"__lang_writer_write": {
		// (w, s_data, s_len) → i32 — heap-form
		// Option[IoError]. Writes string bytes to w.fd via
		// fd_write in a loop; returns None on success.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWriterWriteBody,
	},
	"__lang_reader_read_line_fd": {
		// (r: i32) → i32 — heap-form Option[string]. Reads
		// bytes one at a time until '\n' or EOF; returns None
		// if EOF hit before any byte. Named `_fd` to distinguish
		// from the legacy stdin-only `__lang_reader_read_line`
		// (kept around so existing call sites compile while the
		// alias map flips).
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderReadLineFdBody,
	},
	"__lang_reader_read_chunk": {
		// (r, n: i32) → i32 — heap-form Option[string].
		// Single fd_read into an n-byte buffer.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderReadChunkBody,
	},
	"__str_idx": {
		// (base_data, base_len, i) → i32 (byte address). For
		// heap-form strings returns base_data + i directly. For
		// inline strings spills (data, len) to fixed scratch and
		// returns scratch + i so the caller's OpLoadByte reads
		// the correct content byte.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrIdxBody,
	},
	"__arr_idx": {
		// (base, i) → i32 (byte address of element i). 4-byte
		// stride. Bounds-checks against the length prefix at
		// [base - 4]; out-of-range traps via `unreachable`.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdxBody,
	},
	"__arr_idx_1": {
		// (base, i) → byte address. Stride 1 (byte arrays).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx1Body,
	},
	"__arr_idx_2": {
		// (base, i) → byte address. Stride 2 (halfword arrays).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx2Body,
	},
	"__arr_idx_8": {
		// (base, i) → byte address. Stride 8 (i64/f64 arrays).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx8Body,
	},
	"__str_slice": {
		// (base_data, base_len, low, high) → (data, len). Builds
		// a fresh string from a slice of the source. Mirrors the
		// WAT path's $__str_slice: bounds-check, inline-fast-path
		// for new_len ≤ 7, heap copy via memory.copy otherwise.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrSliceBody,
	},
	"__str_eq": {
		// (a_data, a_len, b_data, b_len) → i32 (0 or 1).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrEqBody,
	},
	"__str_concat": {
		// (a_data, a_len, b_data, b_len) → (data, len). Multi-
		// value return for the two-word ABI.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrConcatBody,
	},
	"__http_entry": {
		// (req, out) → () — wasi:http/incoming-handler wrapper.
		// Marshals the canonical-ABI incoming-request into the
		// user's HttpRequest struct, calls handle(), then streams
		// the HttpResponse back. Exported under the canonical
		// `wasi:http/incoming-handler@0.2.0#handle` name from the
		// emit-time export-renaming layer in wasmbin.go.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildHttpEntryBody,
	},
	"__bytes_to_lang_string": {
		// (host_ptr, host_len) → (data, len) — heap-form lang
		// string built by memcpy'ing the host bytes. Used by the
		// http_entry wrapper to materialise method / path / body
		// strings from the canonical-ABI return areas.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildBytesToLangStringBody,
	},
	"cabi_realloc": {
		// (orig_ptr, orig_size, align, new_size) → i32 — the
		// canonical-ABI allocator the host invokes to materialise
		// dynamically-sized return values (e.g. list<u8> for
		// header names / values) in our linear memory. Aligns
		// the bump cursor before forwarding to __lang_alloc.
		// Exported by name (the host looks it up); see wasmbin.go.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildCabiReallocBody,
	},
	"__slice_make": {
		// (data, len) → i32 — 8-byte slice header (data@0, len@4).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceMakeBody,
	},
	"__slice_idx": {
		// Legacy unsuffixed name, stride 4 (default i32 slices).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceIdxBody(4),
	},
	"__slice_idx_1": {
		// (slice, i) → i32 — bounds-checked byte address; stride 1.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceIdxBody(1),
	},
	"__slice_idx_2": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceIdxBody(2),
	},
	"__slice_idx_4": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceIdxBody(4),
	},
	"__slice_idx_8": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceIdxBody(8),
	},
	"__method_string_as_bytes": {
		// (s_data, s_len) → i32 — zero-copy slice header
		// aliasing the string's bytes.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStringAsBytesBody,
	},
	"__lang_stdout": {
		// () → i32 — Writer struct with fd=1 (stdout).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStdoutBody,
	},
	"__lang_stderr": {
		// () → i32 — Writer struct with fd=2 (stderr).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStderrBody,
	},
}

// allocCursorAddr is the memory offset where the bump cursor lives.
// Matches the WAT path's choice of 40. The cursor is 4 bytes;
// memory[40..44] holds the i32 LE pointer to the next free byte.
const allocCursorAddr = 40

// allocMinStart is the minimum value the bump cursor can take —
// must be past the cursor cell itself and any other reserved low-
// memory state. 64 matches the WAT path's floor.
const allocMinStart = 64

// buildStrLenBody assembles the wasm bytes for __lang_str_len.
//
// Signature: (param $data i32) (param $len i32) (result i32)
//
// SSO seam: the top bit of $len discriminates inline (1) vs
// heap (0) form.
//   - inline: byte length lives in bits 24..26 of $len (0..7).
//   - heap:   $len is the byte length directly.
//
// Body (logical):
//
//	if ($len & 0x80000000) != 0 {
//	    ($len >> 24) & 0x7
//	} else {
//	    $len
//	}
//
// Body (wasm):
//
//	local.get 1
//	i32.const 0x80000000
//	i32.and
//	if (result i32)
//	    local.get 1
//	    i32.const 24
//	    i32.shr_u
//	    i32.const 0x7
//	    i32.and
//	else
//	    local.get 1
//	end
// buildAllocBody assembles the wasm bytes for __lang_alloc.
//
// Signature: (param $size i32) (result i32)
//
// Body: bump cursor at memory[40]. Returns the OLD cursor, bumps
// to (cursor + size), and grows memory if the new end exceeds
// current size.
//
// Logical:
//
//	ptr  = mem[40]
//	end  = ptr + size
//	need = ((end + 65535) >> 16) - memory.size
//	if need > 0 { memory.grow(need); drop }
//	mem[40] = end
//	return ptr
//
// Wasm locals (in order):
//
//	0: $size  (param)
//	1: $ptr
//	2: $end
//	3: $need
func buildAllocBody(_ map[string]uint32) []byte {
	var body []byte
	// ptr = mem[40]
	body = inst.InstI32Const(body, allocCursorAddr)
	body = memInstI32Load(body)
	body = inst.InstLocalSet(body, 1) // $ptr
	// Round size up to 4 — keeps the bump cursor word-aligned
	// across all callers. Some helpers (path_open / fd_read /
	// fd_write retptrs) pass alloc results to WASI imports that
	// store u32 results there, and wasmtime enforces 4-byte
	// alignment on those host writes. The slack (≤ 3 bytes per
	// alloc) is bounded and the no-free arena means it doesn't
	// fragment over time.
	body = inst.InstLocalGet(body, 0) // $size
	body = inst.InstI32Const(body, 3)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -4)
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, 0)
	// end = ptr + size
	body = inst.InstLocalGet(body, 1) // $ptr
	body = inst.InstLocalGet(body, 0) // $size
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 2) // $end
	// need = ((end + 65535) >> 16) - memory.size
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 65535)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32ShrU(body)
	body = memInstMemorySize(body)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalSet(body, 3) // $need
	// if need > 0 { memory.grow(need); drop }
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32GtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 3)
	body = memInstMemoryGrow(body)
	body = inst.InstDrop(body)
	body = inst.InstEnd(body)
	// mem[40] = end
	body = inst.InstI32Const(body, allocCursorAddr)
	body = inst.InstLocalGet(body, 2) // $end
	body = memInstI32Store(body)
	// return ptr
	body = inst.InstLocalGet(body, 1) // $ptr
	// Locals declaration: three i32 scratch slots (ptr, end, need)
	// after the single i32 param.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrByteBody assembles wasm bytes for __lang_str_byte.
//
// Signature: (param $data i32) (param $len i32) (param $i i32) (result i32)
//
// Logical:
//
//	if ($len & 0x80000000) != 0 {          // inline form
//	    if $i < 4 { ($data >> ($i*8)) & 0xff }
//	    else      { ($len  >> (($i-4)*8)) & 0xff }
//	} else {                                // heap form
//	    i32.load8_u at ($data + $i)
//	}
func buildStrByteBody(_ map[string]uint32) []byte {
	var body []byte
	// inline-vs-heap dispatch
	body = inst.InstLocalGet(body, 1) // $len
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		// inline branch
		body = inst.InstLocalGet(body, 2) // $i
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32LtU(body)
		body = inst.InstIfStart(body, encode.ValtypeI32)
		{
			// ($data >> ($i * 8)) & 0xff
			body = inst.InstLocalGet(body, 0) // $data
			body = inst.InstLocalGet(body, 2) // $i
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 0xff)
			body = numeric.InstI32And(body)
		}
		body = inst.InstElse(body)
		{
			// ($len >> (($i - 4) * 8)) & 0xff
			body = inst.InstLocalGet(body, 1) // $len
			body = inst.InstLocalGet(body, 2) // $i
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Sub(body)
			body = inst.InstI32Const(body, 8)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 0xff)
			body = numeric.InstI32And(body)
		}
		body = inst.InstEnd(body)
	}
	body = inst.InstElse(body)
	{
		// heap branch: i32.load8_u at ($data + $i)
		body = inst.InstLocalGet(body, 0) // $data
		body = inst.InstLocalGet(body, 2) // $i
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load8U(body, 0, 0)
	}
	body = inst.InstEnd(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStrEqBody assembles wasm bytes for __str_eq.
//
// Signature: (param $a_data $a_len $b_data $b_len i32) (result i32)
// Locals (after params): $la (4), $lb (5), $i (6).
//
// Strategy:
//  1. Two-word pair equality fast path — identical (data, len)
//     pairs → equal. Catches both heap (same pointer + same len)
//     and inline (same bit-pattern) coincidences.
//  2. If pair-eq failed and BOTH operands have the inline flag
//     set, they must differ (inline encoding is deterministic).
//  3. Otherwise compare lengths via __lang_str_len. Different
//     lengths → not equal.
//  4. Byte loop via __lang_str_byte (handles inline + heap on
//     both sides transparently).
func buildStrEqBody(idxs map[string]uint32) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	var body []byte
	// Step 1: pair-eq fast path.
	body = inst.InstLocalGet(body, 0) // a_data
	body = inst.InstLocalGet(body, 2) // b_data
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, 1) // a_len
	body = inst.InstLocalGet(body, 3) // b_len
	body = numeric.InstI32Eq(body)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 2: both-inline distinct → return 0.
	body = inst.InstLocalGet(body, 1) // a_len
	body = inst.InstLocalGet(body, 3) // b_len
	body = numeric.InstI32And(body)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 3: la = __lang_str_len(a); lb = __lang_str_len(b); if differ return 0.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4) // $la
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Ne(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)

	// Step 4: byte loop.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $i = 0
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= $la: return 1.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 1)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		// if __str_byte(a, i) != __str_byte(b, i): return 0.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstCall(body, strByte)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstCall(body, strByte)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, 0)
		body = inst.InstReturn(body)
		body = inst.InstEnd(body)
		// $i = $i + 1; continue loop.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	// Loop never falls through (every iteration ends in return
	// or br 0), but wasm validation still wants a terminating
	// instruction with the function's result type. `unreachable`
	// satisfies the verifier without emitting a runtime const.
	body = inst.InstUnreachable(body)

	// Three i32 locals: $la, $lb, $i.
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrConcatBody assembles wasm bytes for __str_concat.
//
// Signature: (param $a_data $a_len $b_data $b_len i32) (result i32 i32)
// Locals (after params): $la (4), $lb (5), $dst (6), $i (7).
//
// Logical:
//
//	la  = __lang_str_len(a)
//	lb  = __lang_str_len(b)
//	dst = __lang_alloc(la + lb)
//	for i in 0..la: mem[dst+i]     = __lang_str_byte(a, i)
//	for i in 0..lb: mem[dst+la+i]  = __lang_str_byte(b, i)
//	return (dst, la + lb)
//
// Result is heap-form (top bit of len clear) regardless of input
// forms; the bytes always land in memory at `dst`.
func buildStrConcatBody(idxs map[string]uint32) []byte {
	strLen := idxs["__lang_str_len"]
	strByte := idxs["__lang_str_byte"]
	alloc := idxs["__lang_alloc"]
	var body []byte
	// la = __lang_str_len(a)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4) // $la
	// lb = __lang_str_len(b)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	// dst = __lang_alloc(la + lb)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6) // $dst
	// Loop 1: i in 0..la — copy a's bytes into mem[dst + i].
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= $la: break (br to enclosing block, label 1).
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// mem[dst + i] = __lang_str_byte(a, i)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i = $i + 1; continue loop.
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// Loop 2: i in 0..lb — copy b's bytes into mem[dst + la + i].
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// addr = dst + la + i
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 7)
		body = numeric.InstI32Add(body)
		// byte = __lang_str_byte(b, i)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstCall(body, strByte)
		body = memory.InstI32Store8(body, 0, 0)
		// $i++
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// Return (dst, la + lb) as the multi-value result.
	body = inst.InstLocalGet(body, 6) // dst (data)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body) // total (len)
	// Four i32 locals: $la, $lb, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

func buildStrLenBody(_ map[string]uint32) []byte {
	var body []byte
	// $len is wasm local 1; $data is local 0 (unused for length).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000)) // 0x80000000 as signed
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 24)
	body = numeric.InstI32ShrU(body)
	body = inst.InstI32Const(body, 0x7)
	body = numeric.InstI32And(body)
	body = inst.InstElse(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstEnd(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildLoadI32Body — i32 (addr) → i32. Single i32.load at offset 0.
// Also used for __load_ptr on wasm32 (heap pointer = i32).
func buildLoadI32Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStoreI32Body — (addr, v) → (). Single i32.store at offset 0.
// Also used for __store_ptr on wasm32.
func buildStoreI32Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0) // addr
	body = inst.InstLocalGet(body, 1) // v
	body = memory.InstI32Store(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildLoadI64Body — (addr) → i64. Single i64.load at offset 0.
func buildLoadI64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI64Load(body, 3, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStoreI64Body — (addr, v) → (). Single i64.store at offset 0.
func buildStoreI64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0) // addr
	body = inst.InstLocalGet(body, 1) // v (i64)
	body = memory.InstI64Store(body, 3, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildPtrWidthBody — () → 4. wasm32's pointer width in bytes.
func buildPtrWidthBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, 4)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildAliasAllocBody — (size) → i32. Calls __lang_alloc; lets
// stdlib reference `__alloc` by name. Raw allocator: no length
// prefix, caller owns the layout (e.g. the Map runtime's mixed
// bucket + entries buffer).
func buildAliasAllocBody(helperIdxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, helperIdxs["__lang_alloc"])
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildRcIncBody — (ptr) → ptr. Refcount inc helper. NULL-safe
// and sentinel-aware (high bit of rc word = "static, never
// touch"). Returns the input pointer unchanged so IR codegen
// can splice an inc into an expression evaluation chain.
// See arm64 emitRcIncRuntime for the canonical implementation
// + docs/RC-PERCEUS-PLAN.md for the rollout.
//
//	if ptr == 0: return ptr
//	rcaddr = ptr - 8
//	rc = mem[rcaddr]
//	if rc & 0x80000000: return ptr ; static sentinel
//	mem[rcaddr] = rc + 1
//	return ptr
func buildRcIncBody(_ map[string]uint32) []byte {
	var body []byte
	// Short-circuit on NULL: leave ptr (= 0) on the stack and return.
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// $rcaddr = ptr - 8.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalTee(body, 1) // $rcaddr (also leaves on stack)
	// $rc = mem[$rcaddr].
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 2) // $rc (also leaves on stack)
	// Static sentinel check — high bit set?
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// mem[$rcaddr] = $rc + 1.
	body = inst.InstLocalGet(body, 1) // rcaddr
	body = inst.InstLocalGet(body, 2) // rc
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	// Return the input pointer.
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRcDecBody — (ptr) → (). Refcount dec helper. NULL-safe
// and sentinel-aware (see buildRcIncBody). Phase-1
// simplification: on rc == 1 the helper still decrements to 0
// instead of calling a type-specific drop handler + freelist
// push. The bump allocator leaks; Phase 3 introduces the real
// freelist and Phase 1e introduces the drop handlers. Until
// then, "freeing" just leaves the slot at rc = 0 so accidental
// re-inc / re-dec stays observable for the leak detector that
// phase 1 testing will rely on.
//
//	if ptr == 0: return
//	rcaddr = ptr - 8
//	rc = mem[rcaddr]
//	if rc & 0x80000000: return    ; static sentinel
//	mem[rcaddr] = rc - 1
func buildRcDecBody(_ map[string]uint32) []byte {
	var body []byte
	// NULL short-circuit: return the (zero) input ptr.
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Defensive low-address guard — Phase 1d-v's exit dec
	// sweep can touch slots holding non-pointer values
	// (enum tags, small i32 literals, stack garbage). On
	// wasm a sub-64-KiB "pointer" would read scratch /
	// .rodata-like regions of linear memory, corrupting
	// whatever lives there. See arm64's emitRcDecRuntime for
	// the matching guard and full rationale.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0x10000)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalTee(body, 1) // $rcaddr
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 2) // $rc
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	// Static-sentinel short-circuit: return the input ptr.
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Store(body, 2, 0)
	// Return the input ptr (preserved through the dec).
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 2, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArrPushGrowBody — (arr, oldLen, stride) → new_data.
// Wasm32 counterpart of arm64.go's emitArrPushGrowRuntime /
// x86_64.go's emitArrPushGrowRuntime. Decides between in-place
// mutation (rc==1 + cap available) and copy-into-new-buffer
// (rc>1 OR cap exhausted) and returns the buffer the caller
// should write the new element into. See
// docs/RC-PERCEUS-PLAN.md "Phase 2".
//
// Locals: 0=arr, 1=oldLen, 2=stride (params); 3=newLen,
// 4=newCap, 5=headerBytes, 6=base.
func buildArrPushGrowBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__lang_alloc"]
	memcpy := helperIdxs["__memcpy"]
	var body []byte
	// Fast path: rc == 1 AND oldLen < cap. Both must hold.
	// rc = mem[arr - 8]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	// cap = mem[arr - 12]; oldLen < cap?
	body = inst.InstLocalGet(body, 1) // oldLen
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32LtS(body)
	// (rc == 1) AND (oldLen < cap)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	// In place: mem[arr - 8] = 2 ; mem[arr - 4] = oldLen + 1 ;
	// return arr.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Copy path. newLen = oldLen + 1.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 3) // $newLen
	// newCap = max(2 * newLen, 4). Use a select.
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Shl(body)
	body = inst.InstLocalTee(body, 4) // $newCap = 2 * newLen
	body = inst.InstI32Const(body, 4)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 4)
	// headerBytes = max(16, stride).
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 16)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 5) // $headerBytes
	// allocSize = headerBytes + newCap * stride.
	// base = __lang_alloc(allocSize) + headerBytes.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 6) // $base = new data ptr
	// mem[base - 12] = newCap
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 8] = 1 (rc; NOT bumped for the copy path)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 4] = newLen
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// memcpy(base, arr, oldLen * stride)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Mul(body)
	body = inst.InstCall(body, memcpy)
	// return base
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildAllocU8Body — (n) → i32. Allocates a length-prefixed
// u8[] of length n. Layout: 16-byte header (Phase 2-prep) —
// pad at data-16, capacity at data-12, refcount at data-8,
// length at data-4, n bytes of payload at data.
//
//	base = __lang_alloc(n + 16) + 16
//	mem[base - 12] = n   // cap (Phase 2-prep)
//	mem[base - 8] = 1    // rc
//	mem[base - 4] = n
//	return base
//
// Stdlib `arr.push` / `s[i]` / __arr_idx_* depend on the
// length prefix being present at -4 for bounds checks.
// See docs/RC-PERCEUS-PLAN.md for the phased rollout.
func buildAllocU8Body(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__lang_alloc"]
	var body []byte
	// base = __lang_alloc(n + 16) + 16
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalTee(body, 1) // $base
	// mem[$base - 12] = n  (cap = n, Phase 2-prep)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	// mem[$base - 8] = 1   (rc = 1)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// mem[$base - 4] = n   (length prefix)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	// return $base
	body = inst.InstLocalGet(body, 1)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildMemcpyBody — (dst, src, n) → (). Emits the wasm
// `memory.copy` instruction (0xFC 0x0A 0x00 0x00). Spec:
//
//	https://webassembly.github.io/spec/core/binary/instructions.html#bulk-memory-instructions
//
// dst and src memory indices are both zero (single-memory wasm 1.0).
func buildMemcpyBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0) // dst
	body = inst.InstLocalGet(body, 1) // src
	body = inst.InstLocalGet(body, 2) // n
	body = append(body, 0xFC, 0x0A, 0x00, 0x00)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildMemsetBody — (dst, b, n) → (). Emits `memory.fill`
// (0xFC 0x0B 0x00). b is treated as a byte (low 8 bits).
func buildMemsetBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0) // dst
	body = inst.InstLocalGet(body, 1) // b
	body = inst.InstLocalGet(body, 2) // n
	body = append(body, 0xFC, 0x0B, 0x00)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStrIdxBody — (base_data, base_len, i) → i32 (byte addr).
// Mirrors the WAT path's $__str_idx: bounds-check against the
// SSO-aware length, then dispatch on inline-vs-heap.
//
//	if i < 0: trap
//	if i >= str_len(base_data, base_len): trap
//	if base_len & 0x80000000:
//	    mem[scratch+0] = base_data
//	    mem[scratch+4] = base_len
//	    return scratch + i
//	else:
//	    return base_data + i
//
// The inline branch spills the (data, len) pair to a fixed
// scratch region so the caller can do a byte load at the
// returned address — the inline content layout puts byte i at
// scratch+i for i in 0..6 (low 4 in data, next 3 in len).
func buildStrIdxBody(helperIdxs map[string]uint32) []byte {
	strLen := helperIdxs["__lang_str_len"]
	var body []byte
	// if i < 0: trap
	body = inst.InstLocalGet(body, 2) // $i
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// if i >= str_len(base_data, base_len): trap
	body = inst.InstLocalGet(body, 2) // $i
	body = inst.InstLocalGet(body, 0) // base_data
	body = inst.InstLocalGet(body, 1) // base_len
	body = inst.InstCall(body, strLen)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// inline-vs-heap dispatch on base_len's top bit.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, encode.ValtypeI32)
	{
		// Spill (data, len) to scratch; return scratch + i.
		body = inst.InstI32Const(body, strIdxScratchAddr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, strIdxScratchAddr+4)
		body = inst.InstLocalGet(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, strIdxScratchAddr)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
	}
	body = inst.InstElse(body)
	{
		// Heap: return base_data + i.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
	}
	body = inst.InstEnd(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildArrIdxBody — (base, i) → i32 (byte addr of element i).
// 4-byte stride. Bounds-check against the i32 length prefix at
// [base-4]; out-of-range traps. Used by stdlib helpers that
// iterate over `_impl`-managed arrays.
func buildArrIdxBody(_ map[string]uint32) []byte {
	var body []byte
	// if i < 0: trap
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// if i >= mem[base-4]: trap
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// return base + i*4
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildArrIdxStride is the common-shape factory used by the
// stride-1/2/8 variants. stride=4 is __arr_idx (buildArrIdxBody).
func buildArrIdxStride(stride int32) func(map[string]uint32) []byte {
	return func(_ map[string]uint32) []byte {
		var body []byte
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32LtS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstUnreachable(body)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = numeric.InstI32GeU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstUnreachable(body)
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 1)
		if stride == 1 {
			body = numeric.InstI32Add(body)
		} else {
			body = inst.InstI32Const(body, stride)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
		}
		return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
	}
}

func buildArrIdx1Body(idxs map[string]uint32) []byte { return buildArrIdxStride(1)(idxs) }
func buildArrIdx2Body(idxs map[string]uint32) []byte { return buildArrIdxStride(2)(idxs) }
func buildArrIdx8Body(idxs map[string]uint32) []byte { return buildArrIdxStride(8)(idxs) }

// buildStringFromBytesBody — (bs) → (data, len). bs is a u8[]
// heap pointer; length lives at [bs-4]. Output is the two-word
// string ABI:
//
//	bLen == 0:  (0, 0x80000000)               inline empty
//	bLen <= 7:  inline-packed (data, len)     no alloc
//	bLen >  7:  heap-form (out, bLen)         alloc + memory.copy
//
// Mirrors the WAT path's $string_from_bytes structure.
//
// Locals (after the one param):
//
//	1: $bLen
//	2: $data (inline pack)
//	3: $len  (inline pack)
//	4: $out  (heap dst)
//	5: $i    (loop counter)
//	6: $byte (per-iteration byte stash — wasm if-blocks can't
//	         read values pushed outside their local scope, so
//	         we save the byte to a local before the if-dispatch)
func buildStringFromBytesBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__lang_alloc"]
	var body []byte
	// $bLen = mem[bs - 4]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 1) // $bLen
	// if bLen == 0: return (0, 0x80000000)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if bLen <= 7: build inline-packed (data, len).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 7)
	body = numeric.InstI32LeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// Initialise $data = 0, $len = 0; loop i from 0..bLen
		// packing byte i into the appropriate slot.
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 2) // $data = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 3) // $len = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 5) // $i = 0
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// if $i >= $bLen: break
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32GeU(body)
			body = inst.InstBrIf(body, 1)
			// $byte = mem[bs + i]
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load8U(body, 0, 0)
			body = inst.InstLocalSet(body, 6) // $byte
			// pack: if i < 4: data |= byte << (i*8); else: len |= byte << ((i-4)*8)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				// $data |= $byte << (i * 8).
				body = inst.InstLocalGet(body, 6) // $byte
				body = inst.InstLocalGet(body, 5)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Mul(body)
				body = numeric.InstI32Shl(body)
				body = inst.InstLocalGet(body, 2)
				body = numeric.InstI32Or(body)
				body = inst.InstLocalSet(body, 2)
			}
			body = inst.InstElse(body)
			{
				// $len |= $byte << ((i - 4) * 8).
				body = inst.InstLocalGet(body, 6) // $byte
				body = inst.InstLocalGet(body, 5)
				body = inst.InstI32Const(body, 4)
				body = numeric.InstI32Sub(body)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Mul(body)
				body = numeric.InstI32Shl(body)
				body = inst.InstLocalGet(body, 3)
				body = numeric.InstI32Or(body)
				body = inst.InstLocalSet(body, 3)
			}
			body = inst.InstEnd(body)
			// $i++
			body = inst.InstLocalGet(body, 5)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 5)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body) // end loop
		body = inst.InstEnd(body) // end block
		// $len |= bLen << 24 | 0x80000000 (inline flag).
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 24)
		body = numeric.InstI32Shl(body)
		body = numeric.InstI32Or(body)
		body = inst.InstI32Const(body, int32(-0x80000000))
		body = numeric.InstI32Or(body)
		body = inst.InstLocalSet(body, 3)
		// return ($data, $len)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// Heap form: $out = alloc($bLen); memory.copy($out, $bs, $bLen).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4) // $out
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = append(body, 0xFC, 0x0A, 0x00, 0x00) // memory.copy
	// return ($out, $bLen)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 1)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrSliceBody — (base_data, base_len, low, high) → (data, len).
// Mirrors the WAT path's $__str_slice. Bounds-checked slice into a
// fresh string. Inline-fast-path for new_len ≤ 7, heap copy
// otherwise.
//
// Layout: 3 bounds traps (low<0, high>src_len, low>high), then
// new_len = high - low. If 0 → return inline empty. If ≤7 → pack
// inline. Else → alloc + memory.copy.
//
// Locals (after the 4 params):
//
//	4: $src_len
//	5: $new_len
//	6: $out
//	7: $i
//	8: $data (inline pack)
//	9: $len  (inline pack)
//	10: $byte
func buildStrSliceBody(helperIdxs map[string]uint32) []byte {
	strLen := helperIdxs["__lang_str_len"]
	strByte := helperIdxs["__lang_str_byte"]
	alloc := helperIdxs["__lang_alloc"]
	var body []byte
	// $src_len = __lang_str_len(base_data, base_len)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4)
	// low < 0: trap
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// high > src_len: trap
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 4)
	body = numeric.InstI32GtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// low > high: trap
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = numeric.InstI32GtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstUnreachable(body)
	body = inst.InstEnd(body)
	// $new_len = high - low
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalSet(body, 5)
	// if new_len == 0: return (0, 0x80000000)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if new_len <= 7: build inline-packed.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 7)
	body = numeric.InstI32LeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 8) // $data = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 9) // $len = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 7) // $i = 0
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 7)
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32GeU(body)
			body = inst.InstBrIf(body, 1)
			// $byte = __lang_str_byte(base_data, base_len, low + i)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstLocalGet(body, 2)
			body = inst.InstLocalGet(body, 7)
			body = numeric.InstI32Add(body)
			body = inst.InstCall(body, strByte)
			body = inst.InstLocalSet(body, 10) // $byte
			// pack
			body = inst.InstLocalGet(body, 7)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstLocalGet(body, 10) // $byte
				body = inst.InstLocalGet(body, 7)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Mul(body)
				body = numeric.InstI32Shl(body)
				body = inst.InstLocalGet(body, 8)
				body = numeric.InstI32Or(body)
				body = inst.InstLocalSet(body, 8)
			}
			body = inst.InstElse(body)
			{
				body = inst.InstLocalGet(body, 10) // $byte
				body = inst.InstLocalGet(body, 7)
				body = inst.InstI32Const(body, 4)
				body = numeric.InstI32Sub(body)
				body = inst.InstI32Const(body, 8)
				body = numeric.InstI32Mul(body)
				body = numeric.InstI32Shl(body)
				body = inst.InstLocalGet(body, 9)
				body = numeric.InstI32Or(body)
				body = inst.InstLocalSet(body, 9)
			}
			body = inst.InstEnd(body)
			body = inst.InstLocalGet(body, 7)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 7)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body) // end loop
		body = inst.InstEnd(body) // end block
		// $len |= new_len << 24 | 0x80000000
		body = inst.InstLocalGet(body, 9)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 24)
		body = numeric.InstI32Shl(body)
		body = numeric.InstI32Or(body)
		body = inst.InstI32Const(body, int32(-0x80000000))
		body = numeric.InstI32Or(body)
		body = inst.InstLocalSet(body, 9)
		// return ($data, $len)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstLocalGet(body, 9)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// Heap form: $out = alloc($new_len); memory.copy($out, base_data + low, $new_len)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6) // $out
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 5)
	body = append(body, 0xFC, 0x0A, 0x00, 0x00) // memory.copy
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadLineBody — () → i32. Reads bytes from stdin via
// __lang_read_byte, accumulating into a growable u8 buffer
// until '\n' (included) or EOF. Returns a heap pointer to an
// Option[string] box.
//
// Layout (matches the IR's payloadLayout for Option[string]
// on wasm32):
//
//	Some(line) box (16 bytes):
//	  +0..3:   tag = 0
//	  +4..7:   padding (8-byte alignment for the payload)
//	  +8..11:  data pointer
//	  +12..15: len (byte count, top bit clear → heap form)
//
//	None box (4 bytes):
//	  +0..3:   tag = 1
//
// EOF before any byte → None. EOF mid-line → Some(partial).
//
// Locals (no params):
//
//	0: $buf    — current heap buffer base
//	1: $cap    — current capacity in bytes
//	2: $n      — bytes written so far
//	3: $byte   — last byte read (or -1 for EOF)
//	4: $newbuf — replacement buffer when growing
//	5: $box    — Option box pointer for return
//	6: $copy_i — byte-copy loop counter
func buildReadLineBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__lang_alloc"]
	readByte := helperIdxs["__lang_read_byte"]
	var body []byte
	// Initial buf: alloc(64), cap=64, n=0
	body = inst.InstI32Const(body, 64)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 0)
	body = inst.InstI32Const(body, 64)
	body = inst.InstLocalSet(body, 1)
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 2)
	// Read loop.
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// $byte = __lang_read_byte()
		body = inst.InstCall(body, readByte)
		body = inst.InstLocalSet(body, 3)
		// if $byte == -1: break (EOF)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, -1)
		body = numeric.InstI32Eq(body)
		body = inst.InstBrIf(body, 1)
		// Grow if $n == $cap: alloc(cap*2), memory.copy(new, buf, n), buf=new, cap*=2
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			body = inst.InstCall(body, alloc)
			body = inst.InstLocalSet(body, 4) // $newbuf
			// memory.copy($newbuf, $buf, $n)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 2)
			body = append(body, 0xFC, 0x0A, 0x00, 0x00)
			// $buf = $newbuf; $cap *= 2
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalSet(body, 0)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Shl(body)
			body = inst.InstLocalSet(body, 1)
		}
		body = inst.InstEnd(body)
		// $buf[$n] = $byte; $n++
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 3)
		body = memory.InstI32Store8(body, 0, 0)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 2)
		// if $byte == '\n' (10): break (line complete)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstI32Const(body, 10)
		body = numeric.InstI32Eq(body)
		body = inst.InstBrIf(body, 1)
		// continue loop
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	// EOF-with-empty-buf → None: alloc(4), tag=1.
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 5)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Build Some(line) box: alloc(16), tag=0, data, len.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 5)
	// tag = 0
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	// data at +8
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	// len at +12
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStdinBody — () → i32. Allocates a 4-byte Reader struct
// `{ fd: i32 }` with fd=0 (stdin) and returns the pointer.
// Generalises the previous "Reader == sentinel 0" stub so the
// shared `__method_Reader_*` helpers (which dispatch on `r.fd`
// since the file-Reader work in PR #ABC) can treat stdin
// identically to file Readers — `fd_read(0, …)` is the kernel-
// level read from stdin regardless of who's calling.
func buildStdinBody(idxs map[string]uint32) []byte {
	alloc := idxs["__lang_alloc"]
	var body []byte
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0)
	body = inst.InstI32Const(body, 0) // fd = 0 (stdin)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadLineBody — (r) → i32. Delegates to
// __lang_read_line, ignoring the receiver. Lives in the
// helper registry so __method_Reader_read_line's IR call
// site finds a real funcidx; once wasmbin grows TCP / file
// Readers, this dispatches on the receiver's discriminator.
func buildReaderReadLineBody(helperIdxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstCall(body, helperIdxs["__lang_read_line"])
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildReaderCloseBody — (r) → (). No-op: wasmbin's stdin
// Reader doesn't own any resources, so close is just a drop.
// Empty body.
func buildReaderCloseBody(_ map[string]uint32) []byte {
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), nil)
}

// buildArgsBody — () → i32. Builds a length-prefixed string[]
// of all argv entries. Returns the data pointer (one past the
// length prefix). Each entry is a 2-word (data, len) pair in
// heap form: data points into argv_buf, len is the strlen
// (top bit clear).
//
// Shares the wasi_args_* init path with __lang_arg_at via the
// low-memory cache (argsInitAddr / argsCountAddr / argsPtrsAddr).
// After init, the wasi-output scratch slots (argsSizesArgcAddr,
// argsSizesBufAddr) are dead — repurposed here as a cache for
// the built-array data pointer + built-flag.
//
// Locals (no params):
//
//	0: $argc
//	1: $bufsize
//	2: $argv_ptrs
//	3: $argv_buf
//	4: $result_raw
//	5: $result
//	6: $i
//	7: $cstr
//	8: $len
func buildArgsBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__lang_alloc"]
	argsSizes := helperIdxs["wasi_args_sizes_get"]
	argsGet := helperIdxs["wasi_args_get"]
	var body []byte
	// Lazy init: same shape as __lang_arg_at.
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
		body = inst.InstLocalSet(body, 0)
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 1)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 2)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstCall(body, alloc)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 3)
		body = inst.InstCall(body, argsGet)
		body = inst.InstDrop(body)
		body = inst.InstI32Const(body, argsCountAddr)
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsPtrsAddr)
		body = inst.InstLocalGet(body, 2)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstI32Const(body, argsInitAddr)
		body = inst.InstI32Const(body, 1)
		body = memory.InstI32Store(body, 2, 0)
		// Reset the built-flag slot (argsSizesBufAddr) to 0 so
		// the cache check below doesn't see stale bufsize bits.
		body = inst.InstI32Const(body, argsSizesBufAddr)
		body = inst.InstI32Const(body, 0)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// Cache check: if argsSizesBufAddr is 1, return cached array
	// data ptr from argsSizesArgcAddr.
	body = inst.InstI32Const(body, argsSizesBufAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, argsSizesArgcAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Build: result_raw = __lang_alloc(argc * 8 + 4)
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 0) // $argc
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Mul(body)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4) // $result_raw
	// mem[result_raw] = argc (length prefix)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 0)
	// $result = $result_raw + 4
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 5)
	// argv_ptrs = mem[argsPtrsAddr]
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)
	// for i in 0..argc: build (data, len) at result + i*8
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // $i
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if $i >= $argc: break
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 0)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $cstr = mem[argv_ptrs + i*4]
		body = inst.InstLocalGet(body, 2)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 7)
		// $len = 0; while mem[cstr+len] != 0: len++
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
		// Store (cstr, len) at result + i*8
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 7)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 8)
		body = memory.InstI32Store(body, 2, 0)
		// $i++
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	// Cache: argsSizesArgcAddr = $result, argsSizesBufAddr = 1
	body = inst.InstI32Const(body, argsSizesArgcAddr)
	body = inst.InstLocalGet(body, 5)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstI32Const(body, argsSizesBufAddr)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArenaSaveBody — () → i32. Returns mem[allocCursorAddr]
// (the bump-allocator cursor). Used by lang's `arena_save()`
// to snapshot the heap before a transient allocation phase.
func buildArenaSaveBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, allocCursorAddr)
	body = memInstI32Load(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildArenaRestoreBody — (handle) → (). Writes mem[allocCursorAddr]
// = handle. Effectively rewinds the bump cursor to the value an
// earlier arena_save returned.
func buildArenaRestoreBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, allocCursorAddr)
	body = inst.InstLocalGet(body, 0)
	body = memInstI32Store(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildSqrtF64Body — (f64) → f64. Thin wrapper around the
// wasm-native f64.sqrt instruction. Exposed via the source-
// language method `(x: f64) sqrt()` in std/float.lang which
// calls __sqrt_f64 directly.
func buildSqrtF64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstF64Sqrt(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildAbsF64Body — (f64) → f64 via wasm-native f64.abs.
func buildAbsF64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstF64Abs(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildFloorF64Body — (f64) → f64 via wasm-native f64.floor.
func buildFloorF64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstF64Floor(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildCeilF64Body — (f64) → f64 via wasm-native f64.ceil.
func buildCeilF64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstF64Ceil(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildTruncF64Body — (f64) → f64 via wasm-native f64.trunc.
func buildTruncF64Body(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstF64Trunc(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}
