// Synthetic runtime-helper functions appended to the module after
// the user functions. These exist to implement IR ops (OpAlloc,
// OpStrLen, OpStrEq, OpStrConcat, OpStrLen-byte, the __fern_print
// WASI wrapper, etc.) without forcing every caller to inline the
// same code sequence.
//
// Each helper is gated by a usage scan over prog.Funcs — programs
// that never need a helper pay zero bytes for its body.
// runtimeHelperSpecs keeps the names + bodies + signatures in one
// place so adding a new helper is one entry.

package wasmbin

import (
	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/wasm/convert"
	"github.com/jakechampion/lang/internal/wasm/encode"
	"github.com/jakechampion/lang/internal/wasm/inst"
	"github.com/jakechampion/lang/internal/wasm/memory"
	"github.com/jakechampion/lang/internal/wasm/numeric"
)

// emitFreelistBin appends the size→(capacity, class) binning both
// __fern_alloc and __fern_free must agree on. Emitting it from ONE
// place is the point: alloc has to BUMP at the same capacity free
// BINS at, or a block returns to a class it was never sized for and
// the next allocation from that class hands back a short buffer.
//
// Reads the 16-rounded request from local `size`. Writes:
//
//	capL   — bytes to reserve/charge (== size in the small tier; the
//	         3-significant-bit round-up in the large tier)
//	classL — heads-table slot, or -1 when the block is too large to
//	         recycle (callers must treat negative as "bump only")
//
// `tmpL` is scratch. All four are plain i32 locals.
func emitFreelistBin(body []byte, size, capL, classL, tmpL uint32) []byte {
	// cap = size; class = -1
	body = inst.InstLocalGet(body, size)
	body = inst.InstLocalSet(body, capL)
	body = inst.InstI32Const(body, -1)
	body = inst.InstLocalSet(body, classL)
	// if size < 16: nothing to do (sub-header allocations aren't classed).
	body = inst.InstLocalGet(body, size)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32GeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, size)
		body = inst.InstI32Const(body, freelistSmallMax)
		body = numeric.InstI32LeU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// Small tier: exact fit. class = (size>>4) - 1.
			body = inst.InstLocalGet(body, size)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32ShrU(body)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Sub(body)
			body = inst.InstLocalSet(body, classL)
		}
		body = inst.InstElse(body)
		{
			// Large tier. shift = floor(log2(size)) - 2, so the
			// granularity keeps three significant bits.
			//   tmp = 31 - clz(size) - 2
			body = inst.InstI32Const(body, 29)
			body = inst.InstLocalGet(body, size)
			body = numeric.InstI32Clz(body)
			body = numeric.InstI32Sub(body)
			body = inst.InstLocalSet(body, tmpL)
			// cap = (size + (1<<shift) - 1) & -(1<<shift)
			body = inst.InstLocalGet(body, size)
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalGet(body, tmpL)
			body = numeric.InstI32Shl(body)
			body = numeric.InstI32Add(body)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Sub(body)
			body = inst.InstI32Const(body, 0)
			body = inst.InstI32Const(body, 1)
			body = inst.InstLocalGet(body, tmpL)
			body = numeric.InstI32Shl(body)
			body = numeric.InstI32Sub(body) // -(1<<shift)
			body = numeric.InstI32And(body)
			body = inst.InstLocalSet(body, capL)
			// Re-derive the shift FROM cap: rounding up can carry into
			// the next octave (e.g. 0x1F01 -> 0x2000), and the class
			// must describe the capacity actually reserved.
			body = inst.InstI32Const(body, 29)
			body = inst.InstLocalGet(body, capL)
			body = numeric.InstI32Clz(body)
			body = numeric.InstI32Sub(body)
			body = inst.InstLocalSet(body, tmpL)
			// idx = (shift-9)*4 + ((cap>>shift) - 4)
			//   cap>>shift is the 3-bit mantissa, always in [4,7].
			body = inst.InstLocalGet(body, tmpL)
			body = inst.InstI32Const(body, 9)
			body = numeric.InstI32Sub(body)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Mul(body)
			body = inst.InstLocalGet(body, capL)
			body = inst.InstLocalGet(body, tmpL)
			body = numeric.InstI32ShrU(body)
			body = numeric.InstI32Add(body)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Sub(body)
			body = inst.InstLocalSet(body, tmpL) // tmp = idx
			// class = 128 + idx, but only while idx is in range.
			body = inst.InstLocalGet(body, tmpL)
			body = inst.InstI32Const(body, freelistLargeClasses)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				body = inst.InstLocalGet(body, tmpL)
				body = inst.InstI32Const(body, freelistSmallClasses)
				body = numeric.InstI32Add(body)
				body = inst.InstLocalSet(body, classL)
			}
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
	}
	body = inst.InstEnd(body)
	return body
}

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
// Bodies that call sibling helpers (e.g. __str_eq → __fern_str_len
// + __fern_str_byte) receive a name → funcidx map so the call
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
func scanRuntimeHelpers(prog *ir.Program, opts EmitOptions) runtimeNeeds {
	var needs runtimeNeeds
	for _, fn := range prog.Funcs {
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpDivS, ir.OpRemS:
				// Guarded division: the helper implements the
				// never-trap contract (x/0 = 0, x%0 = x, INT_MIN/-1
				// = INT_MIN, INT_MIN%-1 = 0) so the raw wasm div/rem
				// instruction never faults.
				needs.add(intDivRemHelperName(op.Width == 64, op.Unsigned, op.Kind == ir.OpRemS))
			case ir.OpStrLen:
				needs.add("__fern_str_len")
			case ir.OpAlloc:
				needs.add("__fern_alloc")
			case ir.OpMakeClosure, ir.OpMakeEnv:
				// Both ops allocate the env block via
				// __fern_alloc_rc1 (rc=1 header so the closure
				// is droppable once FuncType locals are rc-
				// tracked); OpMakeClosure also allocs a second
				// pair cell the same way. alloc_rc1 calls
				// __fern_alloc internally.
				needs.add("__fern_alloc")
				needs.add("__fern_alloc_rc1")
			case ir.OpCallDirect, ir.OpRcInc, ir.OpRcDec, ir.OpRcIsUnique:
				// Source-language built-ins lower to OpCallDirect
				// with the source name; the call-site lookup
				// goes through callDirectAlias which routes to
				// the synthetic helper. The trigger here uses
				// the same alias so the helper actually exists.
				// The dedicated rc ops (#4402 opt 2) keep the
				// helper name in Str, so the same scan covers them.
				switch callDirectAlias(op.Str) {
				case "__fern_str_dec":
					// Two-word string-local reclamation (emitDec
					// string branch). Frees the heap buffer at the
					// last reference; pulls in box_free (→ __free)
					// and rc_dec.
					needs.add("__free")
					needs.add("__fern_box_free")
					needs.add("__fern_rc_dec")
					needs.add("__fern_str_dec")
				case "__fern_str_append":
					// In-place-when-unique string self-append (#5637). Its
					// fallback path calls __str_concat (which allocates via
					// alloc_rc1 and reads bytes via str_len / str_byte) and
					// __fern_str_dec — so pull in both, plus str_dec's own
					// transitive deps (box_free → __free, rc_dec), which the
					// flat scan does not close over.
					needs.add("__fern_str_append")
					needs.add("__str_concat")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_alloc")
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_str_dec")
					needs.add("__fern_box_free")
					needs.add("__fern_rc_dec")
					needs.add("__free")
				case "__fern_cell_free":
					// Map boxed-cell reclamation (the column walk frees
					// each dead K/V cell after str_dec'ing its buffer).
					// Pushes the raw 16-byte cell onto the freelist.
					needs.add("__fern_cell_free")
					needs.add("__free")
				case "__fern_str_inc":
					// Two-word string alias retain (Var / Assign /
					// return-transfer / element-init). Pulls in rc_inc.
					needs.add("__fern_rc_inc")
					needs.add("__fern_str_inc")
				case "__fern_print":
					// fd_write under the hood; transitively
					// pulls in the byte-copy + alloc helpers.
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_alloc")
					needs.add("__fern_print")
				case "__fern_eprint":
					// Same shape as __fern_print but fd=2 (stderr).
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_alloc")
					needs.add("__fern_eprint")
				case "__fern_write":
					// Same shape as __fern_print but no trailing
					// newline (fd=1).
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_alloc")
					needs.add("__fern_write")
				case "__fern_putchar":
					// (b) → () — single-byte write to stdout. The
					// preview-2 body heap-allocates a 1-byte buffer
					// (the preview-1 body uses fixed scratch), so it
					// pulls in __fern_alloc under Preview2WASI.
					if opts.Preview2WASI {
						needs.add("__fern_alloc")
					}
					needs.add("__fern_putchar")
				case "__fern_exit":
					// wasi_proc_exit under the hood; nothing
					// else needed.
					needs.add("__fern_exit")
				case "__fern_random_i32":
					// wasi_random_get under the hood; writes
					// 4 random bytes to the fixed scratch slot
					// and returns them as an i32.
					needs.add("__fern_random_i32")
				case "__fern_map_hash_seed":
					// core/map's per-process string-hash seed —
					// one lazy __fern_random_i32 draw, cached at
					// mapHashSeedAddr.
					needs.add("__fern_random_i32")
					needs.add("__fern_map_hash_seed")
				case "__fern_random_bytes":
					// (n) → (data, len) — wasi_random_get into
					// a fresh n-byte heap allocation. Returns
					// the (data, len) pair of the heap string.
					needs.add("__fern_alloc")
					needs.add("__fern_random_bytes")
				case "__fern_now_ns":
					// wasi_clock_time_get + alloc-per-call for
					// the 8-byte output buffer.
					needs.add("__fern_alloc")
					needs.add("__fern_now_ns")
				case "__fern_now_unix_ms":
					// Same as __fern_now_ns / 1_000_000.
					needs.add("__fern_alloc")
					needs.add("__fern_now_unix_ms")
				case "__fern_monotonic_ns":
					// CLOCK_MONOTONIC (1) variant of __fern_now_ns.
					needs.add("__fern_alloc")
					needs.add("__fern_monotonic_ns")
				case "__fern_wasm_timer_pollable":
					// wasm reactor timer: subscribe-duration → pollable.
					needs.add("__fern_wasm_timer_pollable")
				case "__fern_wasm_block":
					// wasm reactor: block on a pollable.
					needs.add("__fern_wasm_block")
				case "__fern_wasm_pollable_drop":
					// wasm reactor: drop a consumed pollable.
					needs.add("__fern_wasm_pollable_drop")
				case "__fern_wasm_poll":
					// wasm reactor multiplexer: poll(list<pollable>).
					// Needs alloc for the 8-byte return area, and
					// cabi_realloc so the host can lower the returned
					// list<u32> of ready indices into our memory.
					needs.add("__fern_alloc")
					needs.add("cabi_realloc")
					needs.add("__fern_wasm_poll")
				case "__fern_sqrt_f64":
					needs.add("__fern_sqrt_f64")
				case "__fern_abs_f64":
					needs.add("__fern_abs_f64")
				case "__fern_floor_f64":
					needs.add("__fern_floor_f64")
				case "__fern_ceil_f64":
					needs.add("__fern_ceil_f64")
				case "__fern_trunc_f64":
					needs.add("__fern_trunc_f64")
				case "__fern_env_count":
					// wasi_environ_sizes_get + alloc-per-call
					// for the 8-byte output buffer.
					needs.add("__fern_alloc")
					needs.add("__fern_env_count")
				case "__fern_arg_count":
					// wasi_args_sizes_get + alloc-per-call
					// for the 8-byte output buffer.
					needs.add("__fern_alloc")
					needs.add("__fern_arg_count")
				case "__fern_arg_at":
					// wasi_args_sizes_get + wasi_args_get +
					// alloc for the argv_ptrs table + argv buf.
					// One-shot init cached in low memory.
					needs.add("__fern_alloc")
					needs.add("__fern_arg_at")
				case "__fern_args":
					// Builds a string[] of all argv entries.
					// Shares the wasi_args_* init path with
					// __fern_arg_at via the low-memory cache.
					// Each entry is copied into a fresh owned
					// string via __fern_str_copy (no view strings
					// escape), so it carries an rc header.
					needs.add("__fern_alloc")
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_str_copy")
					needs.add("__fern_args")
				case "__fern_env_at":
					// wasi_environ_sizes_get + wasi_environ_get
					// + alloc for the environ_ptrs table + buf.
					// The i-th entry is copied into a fresh owned
					// string via __fern_str_copy.
					needs.add("__fern_alloc")
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_str_copy")
					needs.add("__fern_env_at")
				case "__fern_env":
					// (name) → Option[string]. Walks the cached
					// environ_ptrs comparing each entry's prefix
					// up to '=' against name. The matched value is
					// copied into a fresh owned string via
					// __fern_str_copy.
					needs.add("__fern_alloc")
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_str_copy")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_env")
				case "__fern_read_byte":
					// wasi_fd_read on stdin (fd=0) + alloc for
					// the per-process scratch region.
					needs.add("__fern_alloc")
					needs.add("__fern_read_byte")
				case "__fern_read_line":
					// Reads bytes via __fern_read_byte until '\n'
					// or EOF, accumulates into a growable buffer,
					// then builds an Option[string] heap box. The
					// accumulation buffer is the returned string's
					// data → rc1-headered for reclamation.
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_read_byte")
					needs.add("__fern_read_line")
				case "__fern_stdin":
					// () → i32 — Reader struct with fd=0 (stdin).
					// Backs `stdin()`; the `__method_Reader_*`
					// helpers dispatch on r.fd so the same code
					// path covers stdin and file Readers.
					needs.add("__fern_alloc")
					needs.add("__fern_stdin")
				case "__fern_reader_read_line_fd":
					// (r) → i32 — heap-form Option[string]. Reads
					// from r.fd byte-by-byte until '\n' / EOF. The
					// line buffer is the returned string → rc1.
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_reader_read_line_fd")
				case "__fern_reader_read_chunk":
					// (r, n) → i32 — single fd_read of up to n
					// bytes into a fresh n-byte heap buffer,
					// returned as Some(chunk) string → rc1.
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_reader_read_chunk")
				case "__fern_reader_close_fd":
					// (r) → i32 — fd_close on r.fd; returns
					// Option[IoError].
					needs.add("__fern_alloc")
					needs.add("__build_io_error")
					needs.add("__fern_reader_close_fd")
				case "__fern_writer_close":
					// Same shape as reader_close — Writer struct
					// has identical { fd: i32 } layout.
					needs.add("__fern_alloc")
					needs.add("__build_io_error")
					needs.add("__fern_writer_close")
				case "__fern_writer_write":
					// (w, s_data, s_len) → i32 — fd_write loop
					// over the SSO-normalized content bytes.
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_writer_write")
				case "__fern_open_reader":
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_open_reader")
				case "__fern_open_writer":
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_open_writer")
				case "__fern_open_appender":
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_open_appender")
				case "__fern_string_from_bytes":
					// (bs: u8[]) → (data, len) — copies the byte
					// array's payload into a fresh string. Inline
					// fast-path for len ≤ 7, heap copy otherwise.
					// Heap buffer is rc=1-headered for reclamation.
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_string_from_bytes")
				case "__fern_read_file":
					// (path) → Result[string, IoError]. Pulls in
					// __build_io_error for the error-path variant
					// construction; __fern_str_len / __fern_str_byte
					// are needed to SSO-normalize the path argument
					// before it reaches path_open. WASI imports
					// (path_open / fd_read / fd_close) get added by
					// scanImports below once this helper is in the
					// needs set. The file-content buffer is the
					// returned string → rc1-headered for reclamation.
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_read_file")
				case "__fern_write_file":
					// (path, content) → Option[IoError]. Same
					// __build_io_error / __fern_str_len /
					// __fern_str_byte chain as read_file plus
					// the str-normalize loop reusing them; the
					// scanRuntimeHelpers transitive close still
					// pulls them in via `needs.add` here.
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__build_io_error")
					needs.add("__fern_write_file")
				case "__fern_tcp_listen":
					// (port) → i32 — heap pointer to a 12-byte
					// listener struct (sock, 0, 0), or -errno
					// on failure. Pulls in the __network_handle
					// accessor that caches wasi:sockets/instance-
					// network. WASI imports get added by
					// scanImports below.
					needs.add("__fern_alloc")
					needs.add("__network_handle")
					needs.add("__fern_tcp_listen")
				case "__fern_tcp_accept":
					// (listener) → i32 — heap pointer to a
					// 12-byte connection struct (sock, instream,
					// outstream), or -errno on failure.
					needs.add("__fern_alloc")
					needs.add("__fern_tcp_accept")
				case "__fern_tcp_connect":
					// (host_be, port) → i32 — outbound client; same
					// 12-byte connection struct as accept. Needs the
					// network accessor (like tcp_listen).
					needs.add("__fern_alloc")
					needs.add("__network_handle")
					needs.add("__fern_tcp_connect")
				case "__fern_tcp_pollable":
					// (conn) → i32 — the connection's readiness
					// pollable for reactor fan-out.
					needs.add("__fern_tcp_pollable")
				case "__fern_tcp_recv":
					// (conn, max) → (data, len) — heap-form
					// string with the bytes read. Empty on
					// stream-error / EOF. The result string is
					// rc-headered (alloc_rc1) so __fern_str_dec
					// reclaims it correctly (#2817 class); the
					// retptr scratch uses plain alloc.
					needs.add("__fern_alloc")
					needs.add("__fern_alloc_rc1")
					needs.add("__fern_tcp_recv")
				case "__fern_tcp_send":
					// (conn, data) → i32 — bytes sent, -1 on
					// failure. SSO-normalizes the input string
					// so inline-form data flows through the
					// host's read of (ptr, len).
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_tcp_send")
				case "__fern_tcp_close":
					// (conn) → i32 (always 0). Drops the
					// streams (if non-zero) before the parent
					// tcp-socket to satisfy the canonical-ABI
					// resource-has-children rule.
					needs.add("__fern_tcp_close")
				case "__fern_udp_send":
					// (host, port, data) → i32 — one-shot UDP
					// datagram (create → bind → connect → send →
					// drop). Parses the IPv4 host literal and
					// SSO-normalizes the data string.
					needs.add("__fern_alloc")
					needs.add("__network_handle")
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_udp_send")
				case "__slice_make":
					needs.add("__fern_alloc")
					needs.add("__slice_make")
				case "__slice_range":
					needs.add("__slice_range")
				case "__slice_idx":
					needs.add("__slice_idx")
				case "__slice_idx_1":
					needs.add("__slice_idx_1")
				case "__slice_idx_4":
					needs.add("__slice_idx_4")
				case "__slice_idx_8":
					needs.add("__slice_idx_8")
				case "__method_string_as_bytes":
					needs.add("__fern_alloc")
					needs.add("__fern_str_len")
					needs.add("__method_string_as_bytes")
				case "__fern_stdout":
					needs.add("__fern_alloc")
					needs.add("__fern_stdout")
				case "__fern_stderr":
					needs.add("__fern_alloc")
					needs.add("__fern_stderr")
				}
				// Low-level memory shims the stdlib calls directly
				// (raw OpCallDirect, no callDirectAlias rewrite).
				// Each is a one-instruction wrapper around a wasm
				// load / store / bulk-memory op so stdlib `.fern`
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
				case "poll":
					// `poll(fds, timeout_ms)` readiness builtin. On wasm it
					// forwards to __fern_wasm_poll (wasi:io/poll.poll over the i32
					// tokens as pollable handles) — so pull in that helper and its
					// deps (alloc for the 8-byte return area, cabi_realloc so the
					// host can lower the returned ready-index list into memory).
					// __fern_wasm_poll's presence adds the wasi:io/poll.poll import,
					// which makes the composer wire the io/poll instance (classify.go).
					needs.add("poll")
					needs.add("__fern_alloc")
					needs.add("cabi_realloc")
					needs.add("__fern_wasm_poll")
				case "__alloc", "__alloc_u8":
					needs.add("__fern_alloc")
					needs.add(op.Str)
				case "__free":
					needs.add("__free")
				case "__alloc_reuse":
					needs.add("__alloc_reuse")
					needs.add("__fern_alloc")
					needs.add("__free")
				case "__fern_arr_dec":
					needs.add("__fern_arr_dec")
					needs.add("__free")
					needs.add("__fern_alloc")
				case "__fern_map_drop":
					needs.add("__fern_map_drop")
					if ast.RcFreeEnabled {
						// Flag-on, the drop frees the buf + handle.
						needs.add("__free")
						needs.add("__fern_alloc")
					}
				case "__fern_box_free":
					needs.add("__fern_box_free")
					needs.add("__free")
					needs.add("__fern_alloc")
				case "__fern_closure_drop":
					needs.add("__fern_closure_drop")
					needs.add("__fern_box_free") // called on rc==1
					needs.add("__fern_rc_dec")   // called otherwise
					needs.add("__free")
					needs.add("__fern_alloc")
				case "__memcpy":
					needs.add("__memcpy")
				case "__memset":
					needs.add("__memset")
				case "__fern_rc_inc":
					needs.add("__fern_rc_inc")
				case "__fern_rc_dec":
					needs.add("__fern_rc_dec")
				case "__fern_arr_push_grow":
					needs.add("__fern_arr_push_grow")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
				case "__fern_arr_push_grow_ptr":
					needs.add("__fern_arr_push_grow_ptr")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
					needs.add("__fern_rc_inc")
				case "__fern_arr_push_grow_str":
					needs.add("__fern_arr_push_grow_str")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
					needs.add("__fern_str_inc")
					needs.add("__fern_rc_inc") // str_inc's heap path calls it
				case "__fern_arr_push_grow_move_ptr":
					needs.add("__fern_arr_push_grow_move_ptr")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
					needs.add("__fern_rc_inc")
				case "__fern_arr_push_grow_move_str":
					needs.add("__fern_arr_push_grow_move_str")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
					needs.add("__fern_str_inc")
					needs.add("__fern_rc_inc") // str_inc's heap path calls it
				case "__fern_arr_cow_inplace":
					needs.add("__fern_arr_cow_inplace")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
				case "__fern_arr_cow_inplace_ptr":
					needs.add("__fern_arr_cow_inplace_ptr")
					needs.add("__fern_alloc")
					needs.add("__memcpy")
					needs.add("__fern_rc_inc")
				case "__fern_drop_arr_ptr":
					needs.add("__fern_drop_arr_ptr")
					needs.add("__fern_rc_dec")
					if ast.RcFreeEnabled {
						// Flag-on, the drop frees the buffer at rc==1.
						needs.add("__free")
						needs.add("__fern_alloc")
					}
				case "__fern_drop_arr_str":
					// string[] drop: walks two-word elements calling
					// __fern_str_dec, then frees the buffer (flag-on).
					needs.add("__fern_drop_arr_str")
					needs.add("__fern_rc_dec")
					needs.add("__fern_str_dec")
					// __fern_str_dec's body UNCONDITIONALLY calls
					// __fern_box_free in its rc==1 branch (and box_free calls
					// __free), so both must be present whenever str_dec is —
					// regardless of RcFreeEnabled (the flat scanRuntimeHelpers
					// pass does no transitive-dep closure, so a case that pulls
					// str_dec in must also pull str_dec's own deps). Omitting
					// box_free let helperIdxs["__fern_box_free"] miss → 0, and
					// str_dec's `call 0` then resolved to whatever user function
					// occupied funcidx 0 (a 5-param comparator in the sort_by
					// repro) → "expected i32 but nothing on stack" invalid wasm
					// (#4816). Mirrors the direct "__fern_str_dec" case above.
					needs.add("__fern_box_free")
					needs.add("__free")
					if ast.RcFreeEnabled {
						needs.add("__fern_alloc")
					}
				case "__fern_rc_is_unique":
					needs.add("__fern_rc_is_unique")
				case "__fern_rc_underflow_count":
					needs.add("__fern_rc_underflow_count")
				case "__fern_arr_push_shared_count":
					needs.add("__fern_arr_push_shared_count")
				case "__fern_arr_push_shared_bytes":
					needs.add("__fern_arr_push_shared_bytes")
				case "__fern_heap_bump_bytes":
					needs.add("__fern_heap_bump_bytes")
				case "__str_idx":
					// Same byte-fetch SSO seam used by
					// __fern_str_byte but returns a byte
					// address that the caller's OpLoadByte
					// dereferences. Used in __map_hash's
					// string-key path.
					needs.add("__fern_str_len")
					needs.add("__str_idx")
				case "__arr_idx":
					// (base, i) → byte address of element i
					// in a 4-byte-stride array. Length prefix
					// at [base-4].
					needs.add("__arr_idx")
				case "__arr_idx_1":
					// Stride-1 byte-array indexing.
					needs.add("__arr_idx_1")
				case "__arr_idx_8":
					// Stride-8 i64 / f64 array indexing.
					needs.add("__arr_idx_8")
				case "__arr_idx_nc":
					needs.add("__arr_idx_nc")
				case "__arr_idx_1_nc":
					needs.add("__arr_idx_1_nc")
				case "__arr_idx_8_nc":
					needs.add("__arr_idx_8_nc")
				case "__str_slice":
					// (base_data, base_len, low, high) → (data, len)
					// — copy bytes [low..high] into a fresh string.
					// Heap buffer is rc=1-headered for reclamation.
					needs.add("__fern_str_len")
					needs.add("__fern_str_byte")
					needs.add("__fern_alloc") // rc1 calls it
					needs.add("__fern_alloc_rc1")
					needs.add("__str_slice")
				}
			case ir.OpStrEq:
				// __str_eq's inline-side byte reads route
				// through __fern_str_byte, and the length
				// dispatch uses __fern_str_len.
				needs.add("__fern_str_len")
				needs.add("__fern_str_byte")
				needs.add("__str_eq")
			case ir.OpStrConcat:
				// __str_concat allocates a buffer sized by
				// the sum of the two operand lengths, then
				// copies bytes one-at-a-time via the SSO-
				// aware byte fetch. Returns the new (data,
				// len) pair as a heap-form string. The buffer
				// carries an rc=1 header (__fern_alloc_rc1) so
				// owned string locals can reclaim it.
				needs.add("__fern_str_len")
				needs.add("__fern_str_byte")
				needs.add("__fern_alloc") // __fern_alloc_rc1 calls it
				needs.add("__fern_alloc_rc1")
				needs.add("__str_concat")
			}
		}
	}
	// Phase 1e-enums-runtime: these runtime helpers build Option /
	// Result / IoError boxes through __fern_alloc_box, which
	// prepends the 8-byte static-sentinel rc header so a future
	// enum-ii predicate widening can run __fern_rc_inc/dec on the
	// boxes safely (they short-circuit on the high bit).
	// __fern_alloc_box calls __fern_alloc internally — already in
	// the set, since every one of these also allocates directly.
	for _, h := range []string{
		"__fern_env", "__fern_read_line", "__build_io_error",
		"__fern_read_file", "__fern_write_file",
		"__fern_open_reader", "__fern_open_writer", "__fern_open_appender",
		"__fern_reader_close_fd", "__fern_writer_close",
		"__fern_writer_write", "__fern_reader_read_line_fd",
		"__fern_reader_read_chunk",
	} {
		if needs.set[h] {
			needs.add("__fern_alloc_box")
			break
		}
	}
	return needs
}

// runtimeHelperSpecs is the registry. Keyed by the canonical
// helper name; the entry's body() builds the wasm bytes lazily.
// intDivRemHelperName maps an (i64?, unsigned?, rem?) shape to the
// runtime-helper name that implements the never-trap contract for
// that division. The wasm div/rem instructions trap on a zero
// divisor (and the signed forms on INT_MIN / -1); the helper
// sanitises the divisor so the hardware op can't fault, then
// selects the contract result: x / 0 = 0, x % 0 = x, INT_MIN / -1
// = INT_MIN, INT_MIN % -1 = 0.
func intDivRemHelperName(w64, unsigned, isRem bool) string {
	op := "idiv"
	if isRem {
		op = "irem"
	}
	sign := "s"
	if unsigned {
		sign = "u"
	}
	width := "32"
	if w64 {
		width = "64"
	}
	return "__fern_" + op + "_" + sign + width
}

// buildIntDivRemBody emits a guarded div/rem helper body. Params
// (locals 0, 1) are the dividend and divisor; local 2 holds the
// sanitised divisor. The sanitised divisor is 1 whenever the real
// divisor would fault (== 0, or the signed INT_MIN / -1 overflow),
// which also yields the right answers for those cases: lhs / 1 =
// lhs = INT_MIN for the overflow quotient, and lhs % 1 = 0 for the
// overflow remainder. A final `select` substitutes the divide-by-
// zero result (0 for div, the dividend for rem).
func buildIntDivRemBody(w64, unsigned, isRem bool) func(map[string]uint32) []byte {
	return func(_ map[string]uint32) []byte {
		vt := encode.ValtypeI32
		if w64 {
			vt = encode.ValtypeI64
		}
		eqz := numeric.InstI32Eqz
		eq := numeric.InstI32Eq
		ne := numeric.InstI32Ne
		// `and` / `or` combine i32 comparison results (eqz / eq / ne
		// all yield i32 booleans), so they stay i32 even for the
		// i64 helpers.
		and := numeric.InstI32And
		or := numeric.InstI32Or
		konst := func(b []byte, v int64) []byte { return inst.InstI32Const(b, int32(v)) }
		minConst := func(b []byte) []byte { return inst.InstI32Const(b, int32(-0x80000000)) }
		divOp := numeric.InstI32DivS
		switch {
		case w64 && unsigned && isRem:
			divOp = numeric.InstI64RemU
		case w64 && unsigned:
			divOp = numeric.InstI64DivU
		case w64 && isRem:
			divOp = numeric.InstI64RemS
		case w64:
			divOp = numeric.InstI64DivS
		case unsigned && isRem:
			divOp = numeric.InstI32RemU
		case unsigned:
			divOp = numeric.InstI32DivU
		case isRem:
			divOp = numeric.InstI32RemS
		}
		if w64 {
			eqz = numeric.InstI64Eqz
			eq = numeric.InstI64Eq
			ne = numeric.InstI64Ne
			konst = inst.InstI64Const
			minConst = func(b []byte) []byte { return inst.InstI64Const(b, int64(-0x8000000000000000)) }
		}

		locals := inst.PutLocalsOneGroup(nil, 1, vt) // local 2 = safe divisor
		var b []byte
		// bad = (rhs == 0)  [ | (lhs == INT_MIN & rhs == -1) for signed ]
		b = inst.InstLocalGet(b, 1)
		b = eqz(b)
		if !unsigned {
			b = inst.InstLocalGet(b, 0)
			b = minConst(b)
			b = eq(b)
			b = inst.InstLocalGet(b, 1)
			b = konst(b, -1)
			b = eq(b)
			b = and(b)
			b = or(b)
		}
		// safe = bad ? 1 : rhs
		b = inst.InstIfStart(b, vt)
		b = konst(b, 1)
		b = inst.InstElse(b)
		b = inst.InstLocalGet(b, 1)
		b = inst.InstEnd(b)
		b = inst.InstLocalSet(b, 2)
		// q = lhs <op> safe
		b = inst.InstLocalGet(b, 0)
		b = inst.InstLocalGet(b, 2)
		b = divOp(b)
		// select(q, zeroCase, rhs != 0): rem's zero-case is the
		// dividend (x % 0 = x), div's is 0.
		if isRem {
			b = inst.InstLocalGet(b, 0)
		} else {
			b = konst(b, 0)
		}
		b = inst.InstLocalGet(b, 1)
		b = konst(b, 0)
		b = ne(b)
		b = inst.InstSelect(b)
		return inst.PutFunctionBody(nil, locals, b)
	}
}

func intDivRemSpec(w64, unsigned, isRem bool) runtimeHelperSpec {
	vt := encode.ValtypeI32
	if w64 {
		vt = encode.ValtypeI64
	}
	return runtimeHelperSpec{
		params:  []byte{vt, vt},
		results: []byte{vt},
		body:    buildIntDivRemBody(w64, unsigned, isRem),
	}
}

var runtimeHelperSpecs = map[string]runtimeHelperSpec{
	"__fern_idiv_s32": intDivRemSpec(false, false, false),
	"__fern_idiv_u32": intDivRemSpec(false, true, false),
	"__fern_irem_s32": intDivRemSpec(false, false, true),
	"__fern_irem_u32": intDivRemSpec(false, true, true),
	"__fern_idiv_s64": intDivRemSpec(true, false, false),
	"__fern_idiv_u64": intDivRemSpec(true, true, false),
	"__fern_irem_s64": intDivRemSpec(true, false, true),
	"__fern_irem_u64": intDivRemSpec(true, true, true),
	"__fern_str_len": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32}, // (data, len)
		results: []byte{encode.ValtypeI32},
		body:    buildStrLenBody,
	},
	"__fern_alloc": {
		params:  []byte{encode.ValtypeI32}, // size
		results: []byte{encode.ValtypeI32}, // pointer
		body:    buildAllocBody,
	},
	"__fern_alloc_box": {
		params:  []byte{encode.ValtypeI32}, // payload size
		results: []byte{encode.ValtypeI32}, // data pointer (base + 8)
		body:    buildAllocBoxBody,
	},
	"__fern_alloc_rc1": {
		params:  []byte{encode.ValtypeI32}, // payload size
		results: []byte{encode.ValtypeI32}, // data pointer (base + 8)
		body:    buildAllocRc1Body,
	},
	"__fern_str_byte": {
		// (data, len, i) → i32 byte; inline-or-heap aware.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrByteBody,
	},
	"__fern_print": {
		// (data, len) → ()
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildPrintBody,
	},
	"__fern_eprint": {
		// (data, len) → () — same shape as __fern_print but
		// writes to fd=2 (stderr) instead of fd=1.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildEprintBody,
	},
	"__fern_write": {
		// (data, len) → () — like __fern_print but without
		// the trailing newline. The pair `print` / `write`
		// mirrors Go's fmt.Println / fmt.Print.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildWriteBody,
	},
	"__fern_putchar": {
		// (b) → () — fd_write a single byte to stdout. Uses
		// the print iovec scratch region as a 1-byte buffer.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildPutcharBody,
	},
	"__fern_exit": {
		// (code) → () — never returns, but the wasm signature
		// still has a void result.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildExitBody,
	},
	"__fern_random_i32": {
		// () → i32 — host-supplied random word via wasi_random_get.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildRandomI32Body,
	},
	"__fern_map_hash_seed": {
		// () → i32 — core/map's per-process string-hash seed, drawn once
		// via __fern_random_i32 and cached. See buildMapHashSeedBody.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildMapHashSeedBody,
	},
	"__fern_random_bytes": {
		// (n) → (data, len) — heap-form string of n random
		// bytes via wasi_random_get. Empty (n=0) → inline empty
		// (0, 0x80000000).
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildRandomBytesBody,
	},
	"__fern_now_ns": {
		// () → i64 — nanoseconds since unix epoch from the
		// realtime clock via wasi_clock_time_get.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildNowNsBody,
	},
	"__fern_now_unix_ms": {
		// () → i64 — milliseconds since unix epoch. Calls
		// wasi_clock_time_get (CLOCK_REALTIME) and divides by
		// 1_000_000.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildNowUnixMsBody,
	},
	"__fern_monotonic_ns": {
		// () → i64 — monotonic nanoseconds via
		// wasi_clock_time_get (CLOCK_MONOTONIC = 1).
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildMonotonicNsBody,
	},
	"__fern_wasm_timer_pollable": {
		// (duration_ns: i64) → pollable handle (i32). The wasm
		// reactor's timer primitive: a pollable that fires after
		// the given duration. Preview-2-only (wraps
		// wasi:clocks/monotonic-clock.subscribe-duration); see
		// buildWasmTimerPollableBody.
		params:  []byte{encode.ValtypeI64},
		results: []byte{encode.ValtypeI32},
		body:    buildWasmTimerPollableBody,
	},
	"__fern_wasm_block": {
		// (pollable: i32) → i32 — synchronously block until the
		// pollable is ready, then return 0. Preview-2-only (wraps
		// wasi:io/poll.pollable.block); see buildWasmBlockBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWasmBlockBody,
	},
	"__fern_wasm_pollable_drop": {
		// (pollable: i32) → i32 — drop a pollable handle, then return
		// 0. Lets the reactor free a consumed timer pollable instead
		// of leaking it until component exit. Preview-2-only (wraps
		// wasi:io/poll.[resource-drop]pollable); see
		// buildWasmPollableDropBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWasmPollableDropBody,
	},
	"__fern_wasm_poll": {
		// (pollables: i32[]) → i32 — the reactor multiplexer. Blocks
		// until at least one pollable in the array is ready, then
		// returns the array index of the first ready one, or -1 if
		// the ready list comes back empty. Preview-2-only (wraps
		// wasi:io/poll.poll); see buildWasmPollBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWasmPollBody,
	},
	"__fern_sqrt_f64": {
		// (f64) → f64 — wasm-native f64.sqrt.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildSqrtF64Body,
	},
	"__fern_abs_f64": {
		// (f64) → f64 — wasm-native f64.abs.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildAbsF64Body,
	},
	"__fern_floor_f64": {
		// (f64) → f64 — wasm-native f64.floor.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildFloorF64Body,
	},
	"__fern_ceil_f64": {
		// (f64) → f64 — wasm-native f64.ceil.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildCeilF64Body,
	},
	"__fern_trunc_f64": {
		// (f64) → f64 — wasm-native f64.trunc.
		params:  []byte{encode.ValtypeF64},
		results: []byte{encode.ValtypeF64},
		body:    buildTruncF64Body,
	},
	"__fern_env_count": {
		// () → i32 — count of environment variables (envc).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildEnvCountBody,
	},
	"__fern_arg_count": {
		// () → i32 — count of command-line args (argc).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArgCountBody,
	},
	"__fern_arg_at": {
		// (i) → (data, len) — the i-th argv string. (0, 0)
		// for i out of [0..argc). Lazily inits + caches argv
		// in low memory on first call.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildArgAtBody,
	},
	"__fern_args": {
		// () → i32 — length-prefixed string[] of all argv
		// entries. Returns the data pointer (length lives at
		// data - 4). Each entry is a 2-word (data, len) pair
		// in heap form (top bit of len clear). Cached after
		// first build.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArgsBody,
	},
	"__fern_env_at": {
		// (i) → (data, len) — the i-th environ entry as a
		// "KEY=VALUE" string. (0, 0) for i out of range.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildEnvAtBody,
	},
	"__fern_env": {
		// (name_data, name_len) → Option[string] heap box.
		// Walks the cached environ_ptrs comparing each
		// entry's prefix up to '=' with name. Returns
		// Some(value) on match, None otherwise.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildEnvBody,
	},
	"__fern_read_byte": {
		// () → i32 — one byte from stdin (0..255), or -1 on
		// EOF/error. Lazily alloc()s a 16-byte scratch region
		// for the iovec + nread out + the 1-byte read buffer.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildReadByteBody,
	},
	"__fern_read_line": {
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
	"__fern_stdin": {
		// () → i32 — constant sentinel Reader. wasmbin doesn't
		// yet model TCP / file Readers (no `tcp_listen` / file
		// preopens), so the value is opaque and only the
		// stdin-specific Reader methods consume it.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStdinBody,
	},
	"__fern_reader_read_line": {
		// (r) → i32 — Reader.read_line(). For wasmbin's stdin-
		// only Reader model, ignores the receiver and delegates
		// to __fern_read_line.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderReadLineBody,
	},
	"__fern_reader_close": {
		// (r) → () — no-op. Drops the receiver. Real Reader.close
		// (file fds, TCP sockets) will need a discriminator-
		// aware path once those Readers exist.
		params:  []byte{encode.ValtypeI32},
		results: nil,
		body:    buildReaderCloseBody,
	},
	"__fern_string_from_bytes": {
		// (bs) → (data, len) — copies bs's payload into a
		// fresh string. Empty array → inline empty; ≤7 bytes →
		// inline-packed; >7 bytes → heap copy via memory.copy.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStringFromBytesBody,
	},
	"__fern_str_copy": {
		// (ptr, len) → (data, len) — copies `len` raw bytes at `ptr`
		// into a fresh owned two-word string (inline ≤7, else rc1 heap
		// copy). Turns a borrowed VIEW string (argv_buf / environ slice,
		// no per-string rc header) into a normal headered string.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrCopyBody,
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
		// targets (the same .fern code runs on arm64).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildPtrWidthBody,
	},
	"poll": {
		// (fds: i32, timeout_ms: i32) → i32 — the readiness builtin. On wasm
		// it forwards to __fern_wasm_poll (wasi:io/poll.poll over the i32
		// tokens as pollable handles), returning the array index of the first
		// ready one (or -1). The `timeout_ms` arg is ignored for this cut (a
		// host timeout would add a timer pollable to the set). std/async's
		// combinators only call `poll` when a `Pending` future exists, and on
		// wasm a `Pending` carries a real pollable handle, so the tokens are
		// always valid pollables (no trap). Referencing `poll` pulls in the
		// __fern_wasm_poll helper → the wasi:io/poll.poll import → the
		// composer wires the io/poll instance automatically (classify.go).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildPollWasmBody,
	},
	"__alloc": {
		// (size) → i32 — same as __fern_alloc. Lives in the
		// registry for stdlib parity.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildAliasAllocBody,
	},
	"__free": {
		// (base, size) → (). Phase 3 step-4 freelist return path.
		// No-op unless ast.RcFreeEnabled. See buildFreeBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: nil,
		body:    buildFreeBody,
	},
	"__alloc_reuse": {
		// (token, tokenSize, size) → i32. Phase 5 drop-reuse (FBIP)
		// primitive: reuse the dropped block in place on a size-class
		// match, else free it and allocate afresh. See
		// buildAllocReuseBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildAllocReuseBody,
	},
	"__fern_arr_dec": {
		// (data, stride) → data. Phase 3 step-4 size-aware array
		// dec; frees the buffer at rc==1. See buildArrDecBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrDecBody,
	},
	"__fern_map_drop": {
		// (m) → m. Phase 3 map reclamation handler; on the last
		// reference (rc==1) frees the buf (size = 24 + cap*(4+8))
		// then the 16-byte handle. See buildMapDropBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildMapDropBody,
	},
	"__fern_box_free": {
		// (data, size) → data. Phase 3 struct/enum box reclamation;
		// the IR pre-gates on rc==1, so this just frees base = data-8
		// (size+8) and returns data. See buildBoxFreeBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildBoxFreeBody,
	},
	"__fern_cell_free": {
		// (cell) → cell. Map boxed-cell reclamation: a string/wide
		// K or V is stored in a raw 16-byte freelist-class cell (an
		// 8-byte OpAlloc rounded up by the allocator); the buffer it
		// pointed at is reclaimed separately (__fern_str_dec in the
		// column walk). At the map's last reference the walk frees
		// the now-dead cell back to its 16-byte size class. Unlike
		// __fern_box_free the cell has NO rc header (it's a raw
		// alloc), so free base = cell, size = 16. Returns cell — the
		// uniform-result shape the IR OpDrop relies on. NULL /
		// low-address guarded. See buildCellFreeBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildCellFreeBody,
	},
	"__fern_closure_drop": {
		// (f) → f. Closure env/pair reclamation: at the last
		// reference (rc==1) frees the rc1 block via __fern_box_free
		// (payload size at f-4, stashed by __fern_alloc_rc1); else
		// (rc>1 / static sentinel) dec's via __fern_rc_dec. NULL /
		// low-address guarded. See buildClosureDropBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildClosureDropBody,
	},
	"__fern_str_dec": {
		// (data, len) → data. Two-word string reclamation: inline
		// strings (len top bit) are no-ops; heap strings free at rc==1
		// (box_free, size at data-4) else dec. See buildStrDecBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildStrDecBody,
	},
	"__fern_str_inc": {
		// (data, len) → (data, len). Two-word string retain: inline
		// no-op; heap incs data's rc (rc_inc guards). Returns the pair
		// so the value survives for the alias store. See buildStrIncBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrIncBody,
	},
	"__fern_rc_inc": {
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
	"__fern_rc_dec": {
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
	"__fern_arr_push_grow": {
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
	"__fern_arr_push_grow_ptr": {
		// (arr, oldLen, stride) → new_data. Rc-tracked-pointer-element
		// variant of __fern_arr_push_grow (#3425): identical fast path
		// (rc==1 + capacity → in-place), but the grow COPY also inc's
		// each copied element so the fresh buffer independently OWNS
		// them. The plain helper's raw memcpy left the copy sharing the
		// old buffer's element pointers at unchanged rc; the old
		// buffer's later walk-drop at rc==1 freed elements the copy
		// still referenced (use-after-free). See
		// buildArrPushGrowPtrBody; mirrors __fern_arr_cow_inplace_ptr.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushGrowPtrBody,
	},
	"__fern_arr_push_grow_str": {
		// (arr, oldLen, stride) → new_data. Two-word string[] variant of
		// __fern_arr_push_grow (#3425): the grow COPY __fern_str_inc's
		// each copied (data, len) pair — matching the
		// __fern_drop_arr_str walk that releases them. See
		// buildArrPushGrowStrBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushGrowStrBody,
	},
	"__fern_arr_push_grow_move_ptr": {
		// (arr, oldLen, stride) → new_data. The self-append
		// (`a = a.append(v)`) sibling of __fern_arr_push_grow_ptr: the
		// grow COPY retains the copied elements only when the incoming
		// rc != 1. At rc==1 the assign's buffer-only __fern_arr_dec frees
		// the old buffer without walking, so its element references
		// transfer and a retain would leak one each; at rc>1 an alias
		// still owns that buffer and both walk-drops would otherwise
		// release the shared elements twice (#3457). See
		// buildArrPushGrowMovePtrBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushGrowMovePtrBody,
	},
	"__fern_arr_push_grow_move_str": {
		// (arr, oldLen, stride) → new_data. Two-word string[] sibling of
		// __fern_arr_push_grow_move_ptr — same rc != 1 retain gate, with
		// __fern_str_inc over the (data, len) pairs (#3457). See
		// buildArrPushGrowMoveStrBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushGrowMoveStrBody,
	},
	"__fern_arr_cow_inplace": {
		// (arr, stride) → new_data. Phase 2b mutate-or-copy
		// helper for `arr[i] = v`. Internalises the rc
		// bookkeeping so the IR-side emit doesn't have to
		// coordinate with __fern_rc_dec's low-address guard
		// (which short-circuits on raw wasm where heap
		// addresses sit below 0x10000):
		//   - rc == 1 → return arr unchanged.
		//   - rc >  1 → alloc fresh buffer with the same
		//     cap+len, memcpy the payload, dec arr's rc
		//     (skipping if static sentinel), return new data.
		// See buildArrCowInPlaceBody + docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrCowInPlaceBody,
	},
	"__fern_arr_cow_inplace_ptr": {
		// (arr, stride) → new_data. Pointer-element variant of
		// __fern_arr_cow_inplace: identical fast path (rc==1 → arr,
		// in-place), but on the COPY path (rc>1) it also inc's each
		// copied element so the fresh buffer independently OWNS them.
		// The plain helper's raw memcpy leaves the copy sharing the
		// receiver's element pointers at unchanged rc — a UAF once
		// either array is dropped. See buildArrCowInPlacePtrBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrCowInPlacePtrBody,
	},
	"__fern_drop_arr_ptr": {
		// (ptr, stride) → ptr. Phase 3 step 3 drop handler for
		// arrays of pointer-shaped rc-tracked elements. On the
		// last reference (rc==1) dec's each element, then dec's
		// the array. See buildDropArrPtrBody +
		// docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildDropArrPtrBody,
	},
	"__fern_drop_arr_str": {
		// (ptr, stride) → ptr. Drop handler for string[] (two-word
		// elements, stride=8 on wasm32). On the last reference (rc==1)
		// __fern_str_dec's each (data, len) element, then frees the
		// buffer (flag-on) / dec's. See buildDropArrStrBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildDropArrStrBody,
	},
	"__fern_rc_is_unique": {
		// (ptr) → i32. Phase 3 struct-drop helper: 1 iff ptr is a
		// real, uniquely-owned heap value (non-null, above the
		// low-address guard, not a static sentinel, rc == 1). See
		// buildRcIsUniqueBody + docs/RC-PERCEUS-PLAN.md.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildRcIsUniqueBody,
	},
	"__fern_rc_underflow_count": {
		// () → i32. Phase 3 detector probe. Returns the
		// rc-underflow counter buildRcDecBody bumps at
		// rcUnderflowAddr. The native backends implement the same
		// entry point over a BSS global.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildRcUnderflowCountBody,
	},
	"__fern_arr_push_shared_count": {
		// () → i32. The rc==1 cliff probe. Returns the counter
		// buildArrPushGrowBody bumps at arrPushSharedAddr when it copies
		// a buffer that had room. The native backends implement the same
		// entry point over a BSS global.
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildArrPushSharedCountBody,
	},
	"__fern_arr_push_shared_bytes": {
		// () → i64. The same cliff the counter beside it counts, weighted
		// by the bytes each crossing copied (oldLen * stride, summed at
		// arrPushCopiedAddr). The native backends implement the same entry
		// point over a BSS global.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildArrPushSharedBytesBody,
	},
	"__fern_heap_bump_bytes": {
		// () → i64. Phase 6 measurement probe. Returns the bump
		// high-water mark (cursor at allocCursorAddr − seed at
		// heapBaseAddr). The natives implement the same entry point
		// over __fern_heap_ptr − __fern_heap_base.
		//
		// i64 even though wasm32's cursor is an i32 address: the
		// builtin's declared result is i64 on every target, so the
		// operand stack has to agree. The difference is bridged by a
		// zero-extend in the body, not by a per-target result type.
		params:  nil,
		results: []byte{encode.ValtypeI64},
		body:    buildHeapBumpBytesBody,
	},
	"__alloc_u8": {
		// (n) → i32 — allocates a length-prefixed u8[] of
		// length n. Layout: 4-byte i32 length prefix at
		// [base - 4], then n bytes of payload starting at
		// base. Returns the data pointer (base). The payload is
		// explicitly zero-filled (a reused freelist block may
		// carry stale bytes) so it matches the interpreter's
		// zero-initialised u8[] — issue #2768.
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
	"__fern_read_file": {
		// (path_data, path_len) → i32 — heap-form
		// Result[string, IoError] pointer. path is interpreted
		// relative to the preopen at fd 3 (the standard
		// `wasmtime --dir=…` mapping). See wasi_fs.go for the
		// streaming-read pipeline.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReadFileBody,
	},
	"__fern_write_file": {
		// (path_data, path_len, content_data, content_len) →
		// i32 — heap-form Option[IoError] pointer (None on
		// success, Some(IoError) on error). Truncates the
		// target via O_CREAT|O_TRUNC; same preopen-fd-3
		// convention as __fern_read_file.
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
	"__fern_tcp_listen": {
		// (port: i32) → i32 — heap pointer to a 12-byte
		// listener struct (tcp-socket, 0, 0) on success;
		// -errno on failure. See wasi_tcp.go.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpListenBody,
	},
	"__fern_tcp_accept": {
		// (listener: i32) → i32 — heap pointer to a 12-byte
		// connection struct (tcp-socket, input-stream,
		// output-stream); -errno on failure.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpAcceptBody,
	},
	"__fern_tcp_connect": {
		// (host_be: i32, port: i32) → i32 — heap pointer to a
		// 12-byte connection struct (tcp-socket, input-stream,
		// output-stream), the same shape tcp_accept yields, or
		// -errno on failure. The outbound client. See wasi_tcp.go.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpConnectBody,
	},
	"__fern_tcp_pollable": {
		// (conn: i32) → i32 — a wasi:io/poll pollable for the
		// connection (tcp-socket.subscribe), so std/async can
		// multiplex N connections for overlapped outbound fan-out.
		// See buildTcpPollableBody.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpPollableBody,
	},
	"__fern_tcp_recv": {
		// (conn: i32, max: i32) → (data, len) heap-form
		// string. Empty pair (0, 0) on stream-error / EOF.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildTcpRecvBody,
	},
	"__fern_tcp_send": {
		// (conn, data_data, data_len) → i32 — bytes sent on
		// success, -1 on stream-error. Chunked at 4 KiB to
		// match wasmtime's blocking-write-and-flush cap.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpSendBody,
	},
	"__fern_tcp_close": {
		// (conn: i32) → i32 (always 0). Drops streams +
		// tcp-socket in canonical child-before-parent order.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildTcpCloseBody,
	},
	"__fern_udp_send": {
		// (host_data, host_len, port, data_data, data_len) -> i32 —
		// bytes accepted by the host, or -errno. String args lower to
		// (ptr, len) pairs, so host + data are two i32s each.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildUdpSendBody,
	},
	"__fern_open_reader": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Reader, IoError]. The Reader struct holds a
		// preview-1 fd.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenReaderBody,
	},
	"__fern_open_writer": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Writer, IoError]. Opens with CREATE|TRUNCATE.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenWriterBody,
	},
	"__fern_open_appender": {
		// (path_data, path_len) → i32 — heap-form
		// Result[Writer, IoError]. Opens with CREATE + APPEND.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildOpenAppenderBody,
	},
	"__fern_reader_close_fd": {
		// (r: i32) → i32 — heap-form Option[IoError]. Calls
		// fd_close on the Reader's fd; returns None on success.
		// Named `_fd` to distinguish from the existing
		// `__fern_reader_close` which is the stdin-only stub.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderCloseFdBody,
	},
	"__fern_writer_close": {
		// (w: i32) → i32 — same shape as the Reader close, with
		// a dedicated name so the IR alias map can route
		// `__method_Writer_close` here.
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWriterCloseBody,
	},
	"__fern_writer_write": {
		// (w, s_data, s_len) → i32 — heap-form
		// Option[IoError]. Writes string bytes to w.fd via
		// fd_write in a loop; returns None on success.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildWriterWriteBody,
	},
	"__fern_reader_read_line_fd": {
		// (r: i32) → i32 — heap-form Option[string]. Reads
		// bytes one at a time until '\n' or EOF; returns None
		// if EOF hit before any byte. Named `_fd` to distinguish
		// from the legacy stdin-only `__fern_reader_read_line`
		// (kept around so existing call sites compile while the
		// alias map flips).
		params:  []byte{encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildReaderReadLineFdBody,
	},
	"__fern_reader_read_chunk": {
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
	"__arr_idx_8": {
		// (base, i) → byte address. Stride 8 (i64/f64 arrays).
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx8Body,
	},
	// Bounds-check-elided variants (#4380 lever 3): same address compute
	// minus the trap blocks, emitted when the caller proved the index in
	// range (ForEach desugar's synthetic `iter[idx]`).
	"__arr_idx_nc": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdxNCBody,
	},
	"__arr_idx_1_nc": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx1NCBody,
	},
	"__arr_idx_8_nc": {
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildArrIdx8NCBody,
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
	"__fern_str_append": {
		// (a_data, a_len, b_data, b_len) → (data, len). In-place-when-
		// unique string self-append; CONSUMES a. See
		// buildStrAppendBody.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32, encode.ValtypeI32},
		body:    buildStrAppendBody,
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
		// the bump cursor before forwarding to __fern_alloc.
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
	"__slice_range": {
		// (lo, hi, len) → i32 — slice-construction bounds check
		// (#5419): traps unless 0 <= lo <= hi <= len; returns hi - lo.
		params:  []byte{encode.ValtypeI32, encode.ValtypeI32, encode.ValtypeI32},
		results: []byte{encode.ValtypeI32},
		body:    buildSliceRangeBody,
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
	"__fern_stdout": {
		// () → i32 — Writer struct with fd=1 (stdout).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStdoutBody,
	},
	"__fern_stderr": {
		// () → i32 — Writer struct with fd=2 (stderr).
		params:  nil,
		results: []byte{encode.ValtypeI32},
		body:    buildStderrBody,
	},
}

// buildStrLenBody assembles the wasm bytes for __fern_str_len.
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
//
// buildAllocBody assembles the wasm bytes for __fern_alloc.
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
	// Round size up to 8 — keeps the bump cursor 8-byte aligned
	// across all callers. The heap base is 8-aligned, so 8-rounding
	// every size keeps every returned pointer 8-aligned. This matters
	// for canonical-ABI retptrs that hold an i64/u64/f64: e.g.
	// wasi:clocks/wall-clock.now's record { seconds: u64, … } and the
	// wasi:sockets/udp send/check-send result<u64,…> — wasmtime traps
	// ("pointer not aligned") if their retptr is only 4-aligned, which
	// is reachable once a prior odd-sized alloc has left the cursor at
	// 4-mod-8 (e.g. inside a wasi:http handler). The slack (≤ 7 bytes
	// per alloc) is bounded and the no-free arena doesn't fragment.
	body = inst.InstLocalGet(body, 0) // $size
	if ast.RcFreeEnabled {
		// Round to the freelist's 16-byte class granularity so a
		// freed block's size class matches a same-logical-size alloc
		// (16-rounding is also 8-aligned).
		body = inst.InstI32Const(body, 15)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -16)
	} else {
		body = inst.InstI32Const(body, 7)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -8)
	}
	body = numeric.InstI32And(body)
	body = inst.InstLocalSet(body, 0)
	if ast.RcFreeEnabled {
		// Phase 3 step-4: reuse a freed block of the same class before
		// bumping. emitFreelistBin turns the 16-rounded request (local
		// 0) into the capacity to charge (local 6) and the heads slot
		// (local 7), so this pop and __fern_free's push cannot drift.
		// Locals: 4 = headAddr, 5 = head, 6 = cap, 7 = class, 8 = tmp.
		body = emitFreelistBin(body, 0, 6, 7, 8)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32GeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// headAddr = freelistHeadsAddr + class*4
			body = inst.InstLocalGet(body, 7)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Mul(body)
			body = inst.InstI32Const(body, freelistHeadsAddr)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalTee(body, 4) // $headAddr
			body = memInstI32Load(body)
			body = inst.InstLocalTee(body, 5) // $head
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
				// mem[headAddr] = mem[head]   (pop: heads[idx]=next)
				body = inst.InstLocalGet(body, 4)
				body = inst.InstLocalGet(body, 5)
				body = memInstI32Load(body)
				body = memInstI32Store(body)
				// return head
				body = inst.InstLocalGet(body, 5)
				body = inst.InstReturn(body)
			}
			body = inst.InstEnd(body)
		}
		body = inst.InstEnd(body)
		// Charge the BINNED capacity, not the raw request: a large-tier
		// block must be as big as the class it will be freed into.
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalSet(body, 0)
	}
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
	// Locals: ptr, end, need — plus headAddr, head for the
	// flag-on freelist pop.
	nLocals := uint32(3)
	if ast.RcFreeEnabled {
		nLocals = 8 // + headAddr, head, cap, class, tmp
	}
	locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildFreeBody assembles wasm bytes for __fern_free(base, size) —
// the Phase 3 step-4 freelist return path (wasm mirror of the
// native helpers). When the freelist is enabled it pushes the
// size-byte block at base onto its 16-byte size class's intrusive
// freelist (the successor pointer lives in the block's first 4
// bytes). Blocks outside the 16..2048 class range stay in the bump
// region. When the freelist is disabled it's an empty no-op body so
// a stray __free in a non-step-4 build is harmless.
//
// Signature: (param $base i32) (param $size i32). One i32 local
// ($headAddr) after the two params.
func buildFreeBody(_ map[string]uint32) []byte {
	var body []byte
	if ast.RcFreeEnabled {
		// size = (size + 15) & -16
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 15)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, -16)
		body = numeric.InstI32And(body)
		body = inst.InstLocalSet(body, 1)
		// Bin exactly as __fern_alloc charged. Locals: 2 = headAddr,
		// 3 = cap (unused here — alloc already reserved it), 4 = class,
		// 5 = tmp.
		body = emitFreelistBin(body, 1, 3, 4, 5)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32GeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// headAddr = freelistHeadsAddr + class*4
			body = inst.InstLocalGet(body, 4)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32Mul(body)
			body = inst.InstI32Const(body, freelistHeadsAddr)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 2) // $headAddr
			// mem[base] = mem[headAddr]   (base.next = old head)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 2)
			body = memInstI32Load(body)
			body = memInstI32Store(body)
			// mem[headAddr] = base   (heads[idx] = base)
			body = inst.InstLocalGet(body, 2)
			body = inst.InstLocalGet(body, 0)
			body = memInstI32Store(body)
		}
		body = inst.InstEnd(body)
	}
	nLocals := uint32(1)
	if ast.RcFreeEnabled {
		nLocals = 4 // headAddr, cap, class, tmp
	}
	locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArrDecBody — (data, stride) → data. Phase 3 step-4
// size-aware array dec (wasm mirror of the native helpers).
// Decrements the array's rc and, on the last reference (rc==1),
// returns the BUFFER to the freelist (base = data - headerBytes,
// headerBytes = max(16, stride), size = headerBytes + cap*stride;
// cap at data-12) instead of dec'ing to 0 — it does NOT walk
// elements. Same null / low-address / sentinel / underflow guards
// as buildRcDecBody. Returns data (the caller drops it).
//
// Locals: 0=data, 1=stride (params); 2=rc, 3=headerBytes, 4=cap.
func buildArrDecBody(helperIdxs map[string]uint32) []byte {
	free := helperIdxs["__free"]
	var body []byte
	// null guard → return data
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return data
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// rc = mem[data-8]; sentinel guard → return data
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 2) // $rc
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if rc == 1: free buffer; else: maybe-bump-underflow + dec.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// headerBytes = max(16, stride) → local 3
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 16)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 16)
		body = numeric.InstI32GeS(body)
		body = inst.InstSelect(body)
		body = inst.InstLocalSet(body, 3)
		// cap = mem[data-12] → local 4
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 12)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 4)
		// __free(base = data - headerBytes, size = headerBytes + cap*stride)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 3)
		body = numeric.InstI32Sub(body) // base
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body) // size
		body = inst.InstCall(body, free)
	}
	body = inst.InstElse(body)
	{
		// if rc <= 0: bump the over-release counter.
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32LeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, rcUnderflowAddr)
		body = inst.InstI32Const(body, rcUnderflowAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstEnd(body)
		// mem[data-8] = rc - 1
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalGet(body, 2)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// return data
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildMapDropBody — (m) → m. Phase 3 map reclamation handler (wasm
// mirror of the native helpers). A Map handle `m` has its rc at
// [m-8] and its buf pointer at [m+0]. On the last reference (rc==1)
// the storage returns to the freelist: the buf (size = 24 +
// cap*(4+entryStride), cap at [buf+0], entryStride = 2*ptrW = 8 on
// wasm32) then the 16-byte handle cell (base = m-8). Entry keys/values
// are NOT walked — their accounting is untouched (they leak, as
// before). On rc>1 the handle is dec'd. Same null / low-address /
// sentinel / underflow guards as buildArrDecBody. Returns m.
//
// Locals: 0=m (param); 1=rc, 2=buf, 3=cap.
func buildMapDropBody(helperIdxs map[string]uint32) []byte {
	free := helperIdxs["__free"]
	var body []byte
	// null guard → return m
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return m
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// rc = mem[m-8]; sentinel guard → return m
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 1) // $rc
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if rc == 1: free buf + handle; else maybe-bump-underflow + dec.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// buf = mem[m] → local 2
		body = inst.InstLocalGet(body, 0)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalTee(body, 2)
		// if buf u>= 0x10000 (covers null + low-address): free it.
		body = inst.InstI32Const(body, rcLowAddrGuard)
		body = numeric.InstI32GeU(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// cap = mem[buf] → local 3
			body = inst.InstLocalGet(body, 2)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, 3)
			// __free(base = buf, size = 24 + cap*(4+entryStride=8) = 24 + cap*12)
			body = inst.InstLocalGet(body, 2) // base
			body = inst.InstI32Const(body, ast.MapHeaderBytes)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstI32Const(body, 12)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body) // size
			body = inst.InstCall(body, free)
		}
		body = inst.InstEnd(body)
		// __free(base = m - 8, size = 16) — the handle cell
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)    // base
		body = inst.InstI32Const(body, 16) // size
		body = inst.InstCall(body, free)
	}
	body = inst.InstElse(body)
	{
		// if rc <= 0: bump the over-release counter.
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 0)
		body = numeric.InstI32LeS(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		body = inst.InstI32Const(body, rcUnderflowAddr)
		body = inst.InstI32Const(body, rcUnderflowAddr)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstEnd(body)
		// mem[m-8] = rc - 1
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Store(body, 2, 0)
	}
	body = inst.InstEnd(body)
	// return m
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 3, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildBoxFreeBody — (data, size) → data. Phase 3 struct/enum box
// reclamation (wasm mirror). The IR pre-gates the call on rc==1 and
// has already dropped the box's rc-tracked fields/payloads, so this
// returns the box (base = data - 8 rc header, freed size = size + 8)
// to the freelist and returns data — the uniform-result shape the IR
// OpDrop relies on (a direct void __free call can't provide it on
// wasm). NULL / low-address guards keep a stray call safe.
//
// Locals: 0=data, 1=size (params).
func buildBoxFreeBody(helperIdxs map[string]uint32) []byte {
	free := helperIdxs["__free"]
	var body []byte
	// null guard → return data
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return data
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// __free(base = data - 8, size = size + 8)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body) // base
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body) // size + 8
	body = inst.InstCall(body, free)
	// return data
	body = inst.InstLocalGet(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildCellFreeBody — (cell) → cell. Map boxed-cell reclamation. The
// IR column walk pre-gates on the map's rc==1 and the non-null cell
// pointer, and has already reclaimed the buffer the cell pointed at
// (__fern_str_dec); this returns the raw 16-byte-class cell to the
// freelist and returns cell — the uniform-result shape the IR OpDrop
// relies on. The cell is a raw OpAlloc (no rc header), so the freed
// base IS cell (no data-8 adjustment) and the size is a fixed 16 (an
// 8-byte payload slot rounded to the 16-byte class by both
// __fern_alloc and __free). NULL / low-address guards keep a stray
// call safe.
//
// Locals: 0=cell (param).
func buildCellFreeBody(helperIdxs map[string]uint32) []byte {
	free := helperIdxs["__free"]
	var body []byte
	// null guard → return cell
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return cell
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// __free(base = cell, size = 16)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, free)
	// return cell
	body = inst.InstLocalGet(body, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildClosureDropBody assembles wasm bytes for
// __fern_closure_drop(f) -> f (wasmbin mirror of the native
// backends). At the last reference (rc==1) it frees the closure
// env/pair rc1 block via __fern_box_free (payload size at f-4,
// stashed by __fern_alloc_rc1); otherwise (rc>1 / static sentinel)
// it dec's via __fern_rc_dec. NULL / low-address guarded. Captured
// pointer targets (and a pair's env) leak for now.
func buildClosureDropBody(helperIdxs map[string]uint32) []byte {
	boxFree := helperIdxs["__fern_box_free"]
	rcDec := helperIdxs["__fern_rc_dec"]
	var body []byte
	// null guard → return f
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return f
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0x10000)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// rc = mem[f-8]; if rc == 1 → box_free(f, mem[f-4]); return
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0) // rc
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0) // data (arg0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0) // size (arg1) at f-4
	body = inst.InstCall(body, boxFree)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// else rc != 1 → rc_dec(f); its result is the return value
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, rcDec)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStrDecBody — (data, len) → data. Two-word string reclamation: at
// the owning local's last reference, free the heap buffer if uniquely
// owned, else dec. Inline-form strings (len's top bit set) hold their
// bytes in (data, len) — no heap, nothing to free → return immediately.
// For heap form the logic mirrors __fern_closure_drop on `data`: null /
// low-address / static-sentinel guarded, rc==1 → __fern_box_free(data,
// size@data-4) (the rc1 header stashed the payload size), else
// __fern_rc_dec. Static literals carry the 0x80000000 sentinel (their
// rc read short-circuits in the helpers); the conservative IR wiring
// only ever hands this fresh owned heap strings anyway.
func buildStrDecBody(helperIdxs map[string]uint32) []byte {
	boxFree := helperIdxs["__fern_box_free"]
	rcDec := helperIdxs["__fern_rc_dec"]
	var body []byte
	// Inline form: if (len & 0x80000000) != 0 → return data (no heap).
	body = inst.InstLocalGet(body, 1) // len
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// null guard → return data
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// low-address guard → return data
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0x10000)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// rc = mem[data-8]; if rc == 1 → box_free(data, len); return.
	// The buffer's PAYLOAD size is the byte count `len` (local 1): an owned
	// heap string (concat / slice result) is allocated as
	// __fern_alloc_rc1(len) — base+8 with exactly `len` payload bytes — so
	// __fern_box_free(data, len) frees `len+8` bytes, rounding to the same
	// 16-byte class the alloc came from, returning the block to the
	// freelist for reuse. (The previous read of `mem[data-4]` was an
	// uninitialised header word — alloc_rc1 writes only rc at base+0 — so it
	// passed a garbage size, misrouting the free out of the size class and
	// leaking the buffer: it could never be reused.) The inline-string high
	// bit was already handled above, so `len` here is a real byte count.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0) // rc
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0) // data
	body = inst.InstLocalGet(body, 1) // len (payload byte count)
	body = inst.InstCall(body, boxFree)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// else rc != 1 (incl. sentinel) → rc_dec(data); result is the return.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, rcDec)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildStrIncBody — (data, len) → (data, len). Two-word string retain:
// inline strings (len's top bit) have no heap, return unchanged; heap
// strings inc data's rc via __fern_rc_inc (which null / low-address /
// static-sentinel short-circuits, so literals are no-ops). The (data,
// len) pair is returned so the value stays on the operand stack for the
// alias store that follows the inc.
func buildStrIncBody(helperIdxs map[string]uint32) []byte {
	rcInc := helperIdxs["__fern_rc_inc"]
	var body []byte
	// Inline form: if (len & 0x80000000) != 0 → return (data, len).
	body = inst.InstLocalGet(body, 1) // len
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Heap: rc_inc(data) (guards null / low-addr / sentinel); it returns
	// data, so the result IS the first return word. Push len for the
	// second.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, rcInc)
	body = inst.InstLocalGet(body, 1)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildAllocBoxBody assembles wasm bytes for
// __fern_alloc_box(size) -> data — the wasmbin counterpart of
// the native backends' helper. Allocates `size + 8` via
// __fern_alloc, writes the static-sentinel 0x80000000 at
// `[base + 0]`, and returns the data pointer `base + 8`. Used
// by the runtime helpers that build Option / Result / IoError
// boxes so a future Phase 1e-enums-ii predicate widening can
// call __fern_rc_inc/dec on enum values safely (they
// short-circuit on the high bit).
//
// Signature: (param $size i32) (result i32). One i32 local
// ($base) after the param.
func buildAllocBoxBody(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	var body []byte
	// base = __fern_alloc(size + 8)
	body = inst.InstLocalGet(body, 0) // $size
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1) // $base
	// mem[base] = 0x80000000 (static sentinel)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = memory.InstI32Store(body, 2, 0)
	// return base + 8
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildAllocRc1Body assembles wasm bytes for
// __fern_alloc_rc1(size) -> data — identical to
// __fern_alloc_box but writes a live rc=1 at `[base+0]` instead
// of the immortal 0x80000000 sentinel. Closure env blocks /
// pairs use it so they are real refcounted objects (droppable at
// rc=0 in Phase 3) rather than immortal ones. Returns base+8.
//
// Signature: (param $size i32) (result i32). One i32 local
// ($base) after the param.
func buildAllocRc1Body(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	var body []byte
	// base = __fern_alloc(size + 8)
	body = inst.InstLocalGet(body, 0) // $size
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 1) // $base
	// mem[base] = 1 (live rc)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base+4] = $size — stash the payload size in the unused half
	// of the rc1 header (= data-4) so a drop site can free the block
	// without a separate size header (closure-env reclamation reads
	// it). Harmless for every other rc1 user.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = memory.InstI32Store(body, 2, 4)
	// return base + 8
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrByteBody assembles wasm bytes for __fern_str_byte.
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
//  3. Otherwise compare lengths via __fern_str_len. Different
//     lengths → not equal.
//  4. Byte loop via __fern_str_byte (handles inline + heap on
//     both sides transparently).
func buildStrEqBody(idxs map[string]uint32) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
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

	// Step 3: la = __fern_str_len(a); lb = __fern_str_len(b); if differ return 0.
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
//	la  = __fern_str_len(a)
//	lb  = __fern_str_len(b)
//	dst = __fern_alloc(la + lb)
//	for i in 0..la: mem[dst+i]     = __fern_str_byte(a, i)
//	for i in 0..lb: mem[dst+la+i]  = __fern_str_byte(b, i)
//	return (dst, la + lb)
//
// Result is heap-form (top bit of len clear) regardless of input
// forms; the bytes always land in memory at `dst`.
func buildStrConcatBody(idxs map[string]uint32) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	// rc=1-headered buffer (data = base+8) so an owned string local
	// reclaims it at its last reference; the byte-copy loops below write
	// to this returned data pointer unchanged.
	alloc := idxs["__fern_alloc_rc1"]
	var body []byte
	// la = __fern_str_len(a)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 4) // $la
	// lb = __fern_str_len(b)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	// dst = __fern_alloc(la + lb)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 6) // $dst
	// Copy a's bytes into mem[dst + 0..la], then b's into mem[dst + la..].
	// Heap-form inputs (top len bit clear) are contiguous in linear memory,
	// so a single memory.copy moves them; inline/SSO inputs (top bit set) pack
	// their bytes into the (data,len) words rather than memory, so those keep
	// the per-byte __fern_str_byte loop (≤7 bytes, negligible). #4379 — replaces
	// the unconditional per-byte copy with memory.copy on the common
	// large-string path.
	body = strConcatCopyOne(body, strByte, 0, 1, 4, 6, 0, false) // a → dst
	body = strConcatCopyOne(body, strByte, 2, 3, 5, 6, 4, true)  // b → dst + la
	// Return (dst, la + lb) as the multi-value result.
	body = inst.InstLocalGet(body, 6) // dst (data)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body) // total (len)
	// Four i32 locals: $la, $lb, $dst, $i.
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrAppendBody assembles wasm bytes for __fern_str_append — the
// in-place-when-unique string self-append behind `s = s + piece` (#5637).
//
// Signature: (param $a_data $a_len $b_data $b_len i32) (result i32 i32)
// Locals (after params): $la (4), $lb (5), $total (6), $i (7 —
// strConcatCopyOne's scratch), $out_data (8), $out_len (9).
//
// It CONSUMES `a`: the IR only emits it where the assignment was about to
// overwrite and reclaim that slot, so its dec-on-overwrite is suppressed.
//
//   - Fast path — `a` is a uniquely-held heap buffer (heap form, at/above
//     the rc guard, rc==1) whose grown length still lands in the SAME
//     16-byte allocator class: copy b's bytes into the slack past a's data
//     and hand the same buffer back as (a_data, la+lb). No allocation, no
//     re-copy of the accumulated prefix.
//   - Slow path — anything else (inline/SSO `a`, a literal, a shared
//     buffer, the class boundary crossed): __str_concat, then
//     __fern_str_dec(a) to release the old binding exactly as the
//     suppressed overwrite dec did.
//
// Same-class is the exact capacity test, not a heuristic: an owned heap
// string is __fern_alloc_rc1(len) — `len + 8` bytes rounded to 16 — and
// __fern_str_dec frees it at the CURRENT len, so a growth that keeps
// `(len + 23) & -16` unchanged both fits the block and still frees back to
// the class it was bumped at. The 16-byte rounding is __fern_alloc's only
// under ast.RcFreeEnabled (it rounds to 8 with the freelist compiled out),
// which is fine because the IR only emits calls here under that flag — the
// same flag that makes the fallback's __fern_str_dec a real reclaim.
//
// The slack is the allocator's 16-byte granularity, so an accumulator
// absorbs ~8-16 short appends per allocation instead of one allocation and
// a full re-copy each. It is NOT amortised growth — there is no capacity
// slot in the 8-byte rc header to hold one — so a long accumulator still
// re-copies once per class step; the geometric fix is the string builder of
// #5637 option 2.
func buildStrAppendBody(idxs map[string]uint32) []byte {
	strLen := idxs["__fern_str_len"]
	strByte := idxs["__fern_str_byte"]
	concat := idxs["__str_concat"]
	strDec := idxs["__fern_str_dec"]
	var body []byte
	// $lb = __fern_str_len(b) — b is commonly an inline/SSO piece, so it
	// needs the resolving read; a's length is its raw word once the heap
	// check below passes.
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, strLen)
	body = inst.InstLocalSet(body, 5) // $lb
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	{
		// Inline/SSO `a` (raw len top bit set): no heap buffer to grow.
		body = inst.InstLocalGet(body, 1)
		body = inst.InstI32Const(body, int32(-0x80000000))
		body = numeric.InstI32And(body)
		body = inst.InstBrIf(body, 0)
		// Below the rc guard: a literal in the data segment (or a static
		// closure cell), never rc-owned. See rcLowAddrGuard. This floor
		// is deliberately LOWER than __fern_str_dec's own 0x10000 (a
		// leftover from the WASI layout, which keeps that helper from
		// freeing sub-64K heap strings). The asymmetry is safe in this
		// direction: the fast path frees nothing, it only mutates a
		// buffer the rc says is uniquely held, and the fallback's
		// str_dec keeps its existing (leak-not-free) behaviour there.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, rcLowAddrGuard)
		body = numeric.InstI32LtU(body)
		body = inst.InstBrIf(body, 0)
		// Shared, or the immortal 0x80000000 literal sentinel: rc != 1.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Ne(body)
		body = inst.InstBrIf(body, 0)
		// $la = $a_len (heap form), $total = $la + $lb.
		body = inst.InstLocalGet(body, 1)
		body = inst.InstLocalTee(body, 4) // $la
		body = inst.InstLocalGet(body, 5)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6) // $total
		// Same 16-byte allocator class? (len + 8 + 15) & -16.
		roundedClass := func(b []byte, local uint32) []byte {
			b = inst.InstLocalGet(b, local)
			b = inst.InstI32Const(b, 23)
			b = numeric.InstI32Add(b)
			b = inst.InstI32Const(b, -16)
			return numeric.InstI32And(b)
		}
		body = roundedClass(body, 4)
		body = roundedClass(body, 6)
		body = numeric.InstI32Ne(body)
		body = inst.InstBrIf(body, 0)
		// In place: copy b's bytes to mem[$a_data + $la ..] and return
		// (a_data, total). rc stays 1 — the accumulator's sole owner is
		// still the slot the caller is about to store into.
		body = strConcatCopyOne(body, strByte, 2, 3, 5, 0, 4, true)
		body = inst.InstLocalGet(body, 0)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstReturn(body)
	}
	body = inst.InstEnd(body)
	// Fallback: (out_data, out_len) = __str_concat(a, b), then release a.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstCall(body, concat)
	body = inst.InstLocalSet(body, 9) // $out_len (top of the pair)
	body = inst.InstLocalSet(body, 8) // $out_data
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, strDec)
	body = inst.InstDrop(body)
	body = inst.InstLocalGet(body, 8)
	body = inst.InstLocalGet(body, 9)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// strConcatCopyOne appends one input string's byte-copy block to a
// __str_concat body (see buildStrConcatBody). The write base is $dst (local
// dstLocal), plus local[offLocal] when hasOff (the +la offset for the second
// string). dataLocal/lenLocal are the string's (data, raw-len) params, and
// lenComputed is its resolved byte length (from __fern_str_len). For a
// heap-form string (raw-len top bit clear → contiguous in memory) it emits a
// single memory.copy; for an inline/SSO string (top bit set → bytes live in
// the words) it falls back to the per-byte __fern_str_byte loop using scratch
// local 7 ($i). #4379.
func strConcatCopyOne(body []byte, strByte uint32, dataLocal, lenLocal, lenComputed, dstLocal, offLocal uint32, hasOff bool) []byte {
	// dstBase pushes the write base ($dst [+ off]).
	dstBase := func(b []byte) []byte {
		b = inst.InstLocalGet(b, dstLocal)
		if hasOff {
			b = inst.InstLocalGet(b, offLocal)
			b = numeric.InstI32Add(b)
		}
		return b
	}
	// heap check: (raw_len & 0x80000000) == 0.
	body = inst.InstLocalGet(body, lenLocal)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	// heap: memory.copy(dstBase, data, lenComputed).
	body = dstBase(body)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstLocalGet(body, lenComputed)
	body = memory.InstMemoryCopy(body)
	body = inst.InstElse(body)
	// inline: per-byte loop, $i in 0..lenComputed → mem[dstBase + i].
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // $i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstLocalGet(body, lenComputed)
	body = numeric.InstI32GeS(body)
	body = inst.InstBrIf(body, 1)
	body = dstBase(body)
	body = inst.InstLocalGet(body, 7)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalGet(body, dataLocal)
	body = inst.InstLocalGet(body, lenLocal)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstCall(body, strByte)
	body = memory.InstI32Store8(body, 0, 0)
	body = inst.InstLocalGet(body, 7)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 7)
	body = inst.InstBr(body, 0)
	body = inst.InstEnd(body) // end loop
	body = inst.InstEnd(body) // end block
	body = inst.InstEnd(body) // end if
	return body
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

// buildPollStubBody — (fds, timeout_ms) → i32, the wasm `poll` stub: ignores its
// two params and returns -1 ("no fd ready"). Real wasm readiness is the separate
// wasi:io/poll pollable path; this keeps poll-referencing modules compilable.
// buildPollWasmBody — (fds: i32, timeout_ms: i32) → i32, the wasm `poll`
// builtin. Forwards the fds list-data pointer (param 0) to
// __fern_wasm_poll, which reads its length at fds-4 and multiplexes the
// pollable handles through wasi:io/poll.poll, returning the index of the
// first ready one (or -1). `timeout_ms` (param 1) is ignored for now.
func buildPollWasmBody(idxs map[string]uint32) []byte {
	wp := idxs["__fern_wasm_poll"]
	var body []byte
	body = inst.InstLocalGet(body, 0) // fds (list data ptr)
	body = inst.InstCall(body, wp)    // __fern_wasm_poll(fds) → index | -1
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildAliasAllocBody — (size) → i32. Calls __fern_alloc; lets
// stdlib reference `__alloc` by name. Raw allocator: no length
// prefix, caller owns the layout (e.g. the Map runtime's mixed
// bucket + entries buffer).
func buildAliasAllocBody(helperIdxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, helperIdxs["__fern_alloc"])
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildAllocReuseBody — (token, tokenSize, size) → i32. The Phase 5
// drop-reuse (FBIP) primitive (wasm mirror of the native helpers).
// When `token` is a live block whose 16-byte size class matches
// `size`'s, hands it straight back (in-place reuse: no free, no alloc).
// When `token` is null, or the classes differ, it frees the (non-null)
// dropped block and allocates `size` bytes via __fern_alloc — so a
// mispaired reuse is only ever slower, never unsound (the matching
// class guarantees the reused block is wide enough). Class arithmetic
// ((sz+15)&-16, exact-fit 16..2048 classes) mirrors __fern_alloc /
// __free.
//
// Locals: 0=token, 1=tokenSize, 2=size (params).
func buildAllocReuseBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__fern_alloc"]
	free := helperIdxs["__free"]
	var body []byte
	// if token == 0 → return __fern_alloc(size)
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, alloc)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if class(tokenSize) == class(size) → return token
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 15)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -16)
	body = numeric.InstI32And(body)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 15)
	body = numeric.InstI32Add(body)
	body = inst.InstI32Const(body, -16)
	body = numeric.InstI32And(body)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// mismatch: __free(token, tokenSize); return __fern_alloc(size)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, free)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstCall(body, alloc)
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
	// Defensive low-address guard, mirroring buildRcDecBody. The
	// static OpConstFunc closure cells live in the reserved window
	// [closuresBase=96, 1024); rc-tracking FuncType locals would
	// otherwise inc one of those cells and read scratch / cell bytes
	// at [ptr-8]. Heap objects (alloc / alloc_rc1) sit at >= 1024 and
	// still get tracked. See rcLowAddrGuard.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
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
	body = inst.InstI32Const(body, rcLowAddrGuard)
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
	// Phase 3 underflow detector: a healthy dec operates on rc >= 1
	// (1 -> 0 frees, n -> n-1 otherwise). If rc is already <= 0 here
	// — past the null / low-address / sentinel guards, so it's a
	// genuine heap value — this dec over-releases it: bump
	// mem[rcUnderflowAddr]. (Note: a prior underflow leaves rc = -1,
	// whose high bit trips the sentinel guard above, so each
	// distinct over-release is counted exactly once.)
	body = inst.InstLocalGet(body, 2) // $rc
	body = inst.InstI32Const(body, 0)
	body = numeric.InstI32LeS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, rcUnderflowAddr)
	body = inst.InstI32Const(body, rcUnderflowAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
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
	alloc := helperIdxs["__fern_alloc"]
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
	// Reaching here means rc != 1 OR the buffer was full. If it still had
	// room then rc != 1 was the sole reason, and the copy below is bought
	// entirely by an extra reference — the rc==1 cliff. Count it, so an
	// accumulator that has silently gone quadratic can be seen rather than
	// inferred from an arena exhaustion downstream. See arrPushSharedAddr.
	body = inst.InstLocalGet(body, 1) // oldLen
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0) // cap
	body = numeric.InstI32LtS(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, arrPushSharedAddr)
	body = inst.InstI32Const(body, arrPushSharedAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = memory.InstI32Store(body, 2, 0)
	// Weight the crossing by the bytes it is about to copy — oldLen *
	// stride, accumulated as an i64 at arrPushCopiedAddr. The product is
	// computed in i32 (wasm32 memory bounds it) and zero-extended.
	body = inst.InstI32Const(body, arrPushCopiedAddr)
	body = inst.InstI32Const(body, arrPushCopiedAddr)
	body = memory.InstI64Load(body, 3, 0)
	body = inst.InstLocalGet(body, 1) // oldLen
	body = inst.InstLocalGet(body, 2) // stride
	body = numeric.InstI32Mul(body)
	body = convert.InstI64ExtendI32U(body)
	body = numeric.InstI64Add(body)
	body = memory.InstI64Store(body, 3, 0)
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
	// base = __fern_alloc(allocSize) + headerBytes.
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

// buildArrPushGrowPtrBody — (arr, oldLen, stride) → new_data. The
// rc-tracked-pointer-element variant of buildArrPushGrowBody (#3425):
// identical fast path, but the grow COPY walks the oldLen copied
// elements and __fern_rc_inc's each so the fresh buffer independently
// OWNS its references. Without the retain, the old buffer's later
// walk-drop at rc==1 (__fern_drop_arr_ptr / deep struct walks) dec'd/
// freed elements the grown copy still referenced. Mirrors
// buildArrCowInPlacePtrBody's retain loop (#4187).
//
// Locals: 0=arr, 1=oldLen, 2=stride (params); 3=newLen, 4=newCap,
// 5=headerBytes, 6=base, 7=i.
func buildArrPushGrowPtrBody(helperIdxs map[string]uint32) []byte {
	return arrPushGrowPtrBody(helperIdxs, false)
}

// buildArrPushGrowMovePtrBody — the self-append (`a = a.append(v)`)
// sibling of buildArrPushGrowPtrBody: the retain loop is skipped when
// the incoming rc is 1. See the x86-64 mirror for why "the old buffer
// survives this grow" is exactly the rc != 1 test (#3457).
func buildArrPushGrowMovePtrBody(helperIdxs map[string]uint32) []byte {
	return arrPushGrowPtrBody(helperIdxs, true)
}

func arrPushGrowPtrBody(helperIdxs map[string]uint32, moveForm bool) []byte {
	alloc := helperIdxs["__fern_alloc"]
	memcpy := helperIdxs["__memcpy"]
	rcinc := helperIdxs["__fern_rc_inc"]
	var body []byte
	// Fast path: rc == 1 AND oldLen < cap → in place (rc=2, len++).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32LtS(body)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
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
	body = inst.InstLocalSet(body, 3)
	// newCap = max(2 * newLen, 4).
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Shl(body)
	body = inst.InstLocalTee(body, 4)
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
	body = inst.InstLocalSet(body, 5)
	// base = __fern_alloc(headerBytes + newCap*stride) + headerBytes.
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 6)
	// mem[base - 12] = newCap
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 8] = 1 (rc)
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
	if moveForm {
		// rc==1: the assign's buffer-only __fern_arr_dec frees the old
		// buffer without walking, so its element references transfer and a
		// retain would leak one each. The copy path left the old rc alone.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	}
	// Element-retain loop: __fern_rc_inc each copied pointer element.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if i >= oldLen: break
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// __fern_rc_inc(mem[base + i*stride]); drop result
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstCall(body, rcinc)
		body = inst.InstDrop(body)
		// i = i + 1; continue
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block
	if moveForm {
		body = inst.InstEnd(body) // if rc != 1
	}
	// return base
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArrPushGrowStrBody — (arr, oldLen, stride) → new_data. The
// two-word string[] variant of buildArrPushGrowBody (#3425): the grow
// COPY __fern_str_inc's each copied (data@+0, len@+4) pair — matching
// the buildDropArrStrBody walk that releases them, so a grow copy and
// the old buffer's eventual element walk stay balanced.
//
// Locals: 0=arr, 1=oldLen, 2=stride (params); 3=newLen, 4=newCap,
// 5=headerBytes, 6=base, 7=i.
func buildArrPushGrowStrBody(helperIdxs map[string]uint32) []byte {
	return arrPushGrowStrBody(helperIdxs, false)
}

// buildArrPushGrowMoveStrBody — the self-append sibling of
// buildArrPushGrowStrBody: retain loop skipped at incoming rc 1 (#3457).
func buildArrPushGrowMoveStrBody(helperIdxs map[string]uint32) []byte {
	return arrPushGrowStrBody(helperIdxs, true)
}

func arrPushGrowStrBody(helperIdxs map[string]uint32, moveForm bool) []byte {
	alloc := helperIdxs["__fern_alloc"]
	memcpy := helperIdxs["__memcpy"]
	strinc := helperIdxs["__fern_str_inc"]
	var body []byte
	// Fast path: rc == 1 AND oldLen < cap → in place (rc=2, len++).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32LtS(body)
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
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
	// Copy path — identical to buildArrPushGrowPtrBody up to the walk.
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 3)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Shl(body)
	body = inst.InstLocalTee(body, 4)
	body = inst.InstI32Const(body, 4)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 16)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalGet(body, 5)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 6)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 4)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstLocalGet(body, 2)
	body = numeric.InstI32Mul(body)
	body = inst.InstCall(body, memcpy)
	if moveForm {
		// rc==1 → the old buffer dies buffer-only; its pairs transfer.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Ne(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	}
	// Element-retain loop: __fern_str_inc each copied (data, len) pair.
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 7) // i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if i >= oldLen: break
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// __fern_str_inc(mem[base+i*stride], mem[base+i*stride+4]); drop both
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0) // data
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 4) // len (offset +4)
		body = inst.InstCall(body, strinc)
		body = inst.InstDrop(body)
		body = inst.InstDrop(body)
		// i = i + 1; continue
		body = inst.InstLocalGet(body, 7)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 7)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block
	if moveForm {
		body = inst.InstEnd(body) // if rc != 1
	}
	// return base
	body = inst.InstLocalGet(body, 6)
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArrCowInPlaceBody — (arr, stride) → new_data. Wasm32
// counterpart of arm64.go's emitArrCowInPlaceRuntime + x86_64's.
// Internalises rc bookkeeping so the IR-side emit avoids the
// __fern_rc_dec low-address guard pitfall (heap addresses sit
// below 0x10000 on raw wasm so the guard would skip every dec).
//
// Locals: 0=arr, 1=stride (params); 2=len, 3=cap,
// 4=headerBytes, 5=base.
func buildArrCowInPlaceBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__fern_alloc"]
	memcpy := helperIdxs["__memcpy"]
	var body []byte
	// Fast path: rc == 1 → return arr.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Copy path. Decrement arr's rc inline (skip if static
	// sentinel — high bit set). Load rc word, check high bit,
	// store rc-1 if not sentinel.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstEnd(body)
	// len = mem[arr - 4]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)
	// cap = mem[arr - 12]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)
	// headerBytes = max(16, stride)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 16)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 4)
	// allocSize = headerBytes + cap * stride
	// base = __fern_alloc(allocSize) + headerBytes
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalGet(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 5)
	// mem[base - 12] = cap
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 8] = 1 (new buffer, rc=1)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 4] = len
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	// memcpy(base, arr, len * stride)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Mul(body)
	body = inst.InstCall(body, memcpy)
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 4, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArrCowInPlacePtrBody — (arr, stride) → new_data. Pointer-element
// variant of buildArrCowInPlaceBody. Same fast path (rc==1 → arr) and
// same alloc + memcpy on the copy path, then a per-element __fern_rc_inc
// loop so the fresh buffer owns its own reference to each copied pointer
// element. Mirrors buildDropArrPtrBody's element walk (inc instead of
// dec). Locals: 0=arr, 1=stride (params); 2=len, 3=cap, 4=headerBytes,
// 5=new_data, 6=i.
func buildArrCowInPlacePtrBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__fern_alloc"]
	memcpy := helperIdxs["__memcpy"]
	rcinc := helperIdxs["__fern_rc_inc"]
	var body []byte
	// Fast path: rc == 1 → return arr (in-place; elements already owned).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Copy path. Decrement arr's rc inline (skip if static sentinel).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstEnd(body)
	// len = mem[arr - 4]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 2)
	// cap = mem[arr - 12]
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 3)
	// headerBytes = max(16, stride)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 16)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 16)
	body = numeric.InstI32GeS(body)
	body = inst.InstSelect(body)
	body = inst.InstLocalSet(body, 4)
	// base = __fern_alloc(headerBytes + cap*stride) + headerBytes
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 3)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Mul(body)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalGet(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 5)
	// mem[base - 12] = cap
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 12)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 8] = 1 (rc=1)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// mem[base - 4] = len
	body = inst.InstLocalGet(body, 5)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Sub(body)
	body = inst.InstLocalGet(body, 2)
	body = memory.InstI32Store(body, 2, 0)
	// memcpy(base, arr, len * stride)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Mul(body)
	body = inst.InstCall(body, memcpy)
	// Element-retain loop: inc each copied pointer element so the fresh
	// buffer owns its own reference (mirrors buildDropArrPtrBody's walk).
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 6) // i = 0
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		// if i >= len: break
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 2)
		body = numeric.InstI32GeS(body)
		body = inst.InstBrIf(body, 1)
		// __fern_rc_inc(mem[base + i*stride]); drop result
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstCall(body, rcinc)
		body = inst.InstDrop(body)
		// i = i + 1; continue
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body) // loop
	body = inst.InstEnd(body) // block
	// return new_data
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRcUnderflowCountBody — () → i32. Returns the rc-underflow
// counter at rcUnderflowAddr (bumped by buildRcDecBody). Phase 3
// detector probe; the native backends provide the same entry over
// a BSS global.
func buildRcUnderflowCountBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, rcUnderflowAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildArrPushSharedCountBody — () → i32. Returns the rc==1 cliff counter at
// arrPushSharedAddr (bumped by buildArrPushGrowBody): how many appends copied
// the whole buffer even though it had room, i.e. the copy was bought by an
// extra reference alone. The native backends provide the same entry over a
// BSS global.
func buildArrPushSharedCountBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, arrPushSharedAddr)
	body = memory.InstI32Load(body, 2, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildArrPushSharedBytesBody — () → i64. Returns the cliff WEIGHT at
// arrPushCopiedAddr (accumulated by buildArrPushGrowBody): the bytes the
// crossings the counter counts actually copied. The count says whether
// anything crossed; only this says whether it mattered. The native backends
// provide the same entry over a BSS global.
func buildArrPushSharedBytesBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, arrPushCopiedAddr)
	body = memory.InstI64Load(body, 3, 0)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildHeapBumpBytesBody assembles __fern_heap_bump_bytes: () → i64.
// Returns the bump high-water mark (cursor at allocCursorAddr minus the
// seed at heapBaseAddr) — 0 at start, growing only on fresh bumps and
// flat across freelist reuse. Mirrors the natives' heap_ptr − heap_base.
//
// The difference is computed in i32 (both operands are wasm32 linear-memory
// addresses) and zero-extended to the i64 the builtin declares. Zero-, not
// sign-extend: the cursor is never below the seed, so the difference is a
// non-negative byte count, and a sign-extend would turn a >2 GiB mark into
// the negative number this widening exists to remove.
func buildHeapBumpBytesBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstI32Const(body, allocCursorAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, heapBaseAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = numeric.InstI32Sub(body)
	body = convert.InstI64ExtendI32U(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildDropArrPtrBody — (ptr, stride) → ptr. Phase 3 step 3 drop
// handler for an array whose elements are pointer-shaped
// rc-tracked values (array / struct / enum / closure — NOT string,
// which isn't rc-tracked yet). When this is the LAST reference to
// the array (rc == 1, i.e. about to be released) it walks the
// `len` elements and dec's each — balancing the per-element inc
// the IR emitted at array-literal construction (Phase 1d-viii) —
// then dec's the array itself. All decrements route through
// __fern_rc_dec so its rc arithmetic, sentinel / low-address
// guards, and the underflow counter stay the single chokepoint.
// Returns the input ptr (passthrough) to match __fern_rc_dec's
// stack shape so the caller's OpDrop balances either way.
//
// Locals: 0=ptr, 1=stride (params); 2=len, 3=i.
func buildDropArrPtrBody(helperIdxs map[string]uint32) []byte {
	rcdec := helperIdxs["__fern_rc_dec"]
	var body []byte
	// NULL guard (mem[ptr-8] would trap on a 0 ptr).
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Low-address guard — mirror buildRcDecBody. The exit dec
	// sweep can hand us an array-typed slot that actually holds a
	// non-pointer (enum tag, small i32, never-taken-branch garbage).
	// Reading mem[ptr-8] / mem[ptr-4] on such a value would corrupt
	// the scratch / low-memory region; treat the low 64 KiB as
	// "not a heap object" and pass it through untouched.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Static-sentinel guard: a .rodata / empty-array sentinel has
	// the high bit set in its rc word — never recurse into it.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Only the last reference recurses: if rc == 1, dec each elem.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// len = mem[ptr-4]
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		// i = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// if i >= len: break
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 2)
			body = numeric.InstI32GeS(body)
			body = inst.InstBrIf(body, 1)
			// __fern_rc_dec(mem[ptr + i*stride]); drop result
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstCall(body, rcdec)
			body = inst.InstDrop(body)
			// i = i + 1; continue
			body = inst.InstLocalGet(body, 3)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 3)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body) // loop
		body = inst.InstEnd(body) // block
	}
	body = inst.InstEnd(body) // if rc==1
	nLocals := uint32(2)
	if ast.RcFreeEnabled {
		nLocals = 4 // + headerBytes (4), cap (5)
		// Phase 3 step-4: on the last reference (rc==1) the elements
		// have been dec'd above, so return the buffer to the
		// freelist; otherwise just dec. headerBytes = max(16, stride);
		// base = ptr - headerBytes; size = headerBytes + cap*stride
		// (cap at ptr-12).
		free := helperIdxs["__free"]
		// rc == 1 ?
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// headerBytes = max(16, stride) → local 4
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 16)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 16)
			body = numeric.InstI32GeS(body)
			body = inst.InstSelect(body)
			body = inst.InstLocalSet(body, 4)
			// cap = mem[ptr-12] → local 5
			body = inst.InstLocalGet(body, 0)
			body = inst.InstI32Const(body, 12)
			body = numeric.InstI32Sub(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, 5)
			// __free(base = ptr - headerBytes, size = headerBytes + cap*stride)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Sub(body) // base (arg1)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body) // size (arg2)
			body = inst.InstCall(body, free)
		}
		body = inst.InstElse(body)
		{
			body = inst.InstLocalGet(body, 0)
			body = inst.InstCall(body, rcdec)
			body = inst.InstDrop(body)
		}
		body = inst.InstEnd(body)
		// result = ptr
		body = inst.InstLocalGet(body, 0)
		locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
	// Dec the array itself; __fern_rc_dec returns the ptr, which
	// becomes this helper's return value.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, rcdec)
	locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildDropArrStrBody — (ptr, stride) → ptr. Drop handler for a
// string[] whose two-word elements (stride=8 on wasm32) each need
// __fern_str_dec. Mirrors buildDropArrPtrBody but the per-element walk
// loads (data, len) and reclaims via __fern_str_dec instead of the
// single-word __fern_rc_dec. On the last reference (rc==1) it frees each
// element string then the buffer; otherwise it dec's the array box.
// Same null / low-address / sentinel guards. Returns the input ptr.
//
// Locals: 0=ptr, 1=stride (params); 2=len, 3=i; (+4=headerBytes, 5=cap
// when RcFreeEnabled).
func buildDropArrStrBody(helperIdxs map[string]uint32) []byte {
	rcdec := helperIdxs["__fern_rc_dec"]
	strdec := helperIdxs["__fern_str_dec"]
	var body []byte
	// NULL guard.
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Low-address guard.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Static-sentinel guard (high rc bit set → never recurse).
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Only the last reference (rc==1) reclaims the elements.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		// len = mem[ptr-4]
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalSet(body, 2)
		// i = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 3)
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// if i >= len: break
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 2)
			body = numeric.InstI32GeS(body)
			body = inst.InstBrIf(body, 1)
			// __fern_str_dec(mem[ptr+i*stride], mem[ptr+i*stride+4]); drop
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 0) // data
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 3)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load(body, 2, 4) // len (offset +4)
			body = inst.InstCall(body, strdec)
			body = inst.InstDrop(body)
			// i = i + 1; continue
			body = inst.InstLocalGet(body, 3)
			body = inst.InstI32Const(body, 1)
			body = numeric.InstI32Add(body)
			body = inst.InstLocalSet(body, 3)
			body = inst.InstBr(body, 0)
		}
		body = inst.InstEnd(body) // loop
		body = inst.InstEnd(body) // block
	}
	body = inst.InstEnd(body) // if rc==1
	nLocals := uint32(2)
	if ast.RcFreeEnabled {
		nLocals = 4 // + headerBytes (4), cap (5) below
		free := helperIdxs["__free"]
		// rc == 1 → free the buffer; else dec the array box.
		body = inst.InstLocalGet(body, 0)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Sub(body)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Eq(body)
		body = inst.InstIfStart(body, inst.BlocktypeEmpty)
		{
			// headerBytes = max(16, stride) → local 4
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 16)
			body = inst.InstLocalGet(body, 1)
			body = inst.InstI32Const(body, 16)
			body = numeric.InstI32GeS(body)
			body = inst.InstSelect(body)
			body = inst.InstLocalSet(body, 4)
			// cap = mem[ptr-12] → local 5
			body = inst.InstLocalGet(body, 0)
			body = inst.InstI32Const(body, 12)
			body = numeric.InstI32Sub(body)
			body = memory.InstI32Load(body, 2, 0)
			body = inst.InstLocalSet(body, 5)
			// __free(base = ptr - headerBytes, size = headerBytes + cap*stride)
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 4)
			body = numeric.InstI32Sub(body)
			body = inst.InstLocalGet(body, 4)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32Mul(body)
			body = numeric.InstI32Add(body)
			body = inst.InstCall(body, free)
		}
		body = inst.InstElse(body)
		{
			body = inst.InstLocalGet(body, 0)
			body = inst.InstCall(body, rcdec)
			body = inst.InstDrop(body)
		}
		body = inst.InstEnd(body)
		body = inst.InstLocalGet(body, 0)
		locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
		return inst.PutFunctionBody(nil, locals, body)
	}
	// Flag-off: just dec the array box.
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, rcdec)
	locals := inst.PutLocalsOneGroup(nil, nLocals, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildRcIsUniqueBody — (ptr) → i32. Returns 1 iff ptr is a real,
// uniquely-owned heap value: non-null, above the low-address
// guard, not a static sentinel (high rc bit clear), and rc == 1.
// Otherwise 0. Same guard chain as buildRcDecBody, so it's safe to
// call on a slot that might hold a non-pointer scalar (enum tag,
// stack garbage). Used by the Phase 3 struct drop emission to gate
// recursive field decs on "this is the last reference" without the
// IR having to read [ptr-8] unguarded.
//
// Locals: 0=ptr (param), 1=rc.
func buildRcIsUniqueBody(_ map[string]uint32) []byte {
	var body []byte
	// ptr == 0 → 0
	body = inst.InstLocalGet(body, 0)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// ptr < 0x10000 → 0 (low-address guard)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, rcLowAddrGuard)
	body = numeric.InstI32LtU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// rc = mem[ptr-8]; if rc & 0x80000000 → 0 (static sentinel)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Sub(body)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalTee(body, 1)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = numeric.InstI32And(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// return rc == 1
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 1)
	body = numeric.InstI32Eq(body)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildAllocU8Body — (n) → i32. Allocates a length-prefixed
// u8[] of length n. Layout: 16-byte header (Phase 2-prep) —
// pad at data-16, capacity at data-12, refcount at data-8,
// length at data-4, n bytes of payload at data.
//
//	base = __fern_alloc(n + 16) + 16
//	mem[base - 12] = n   // cap (Phase 2-prep)
//	mem[base - 8] = 1    // rc
//	mem[base - 4] = n
//	return base
//
// Stdlib `arr.push` / `s[i]` / __arr_idx_* depend on the
// length prefix being present at -4 for bounds checks.
// See docs/RC-PERCEUS-PLAN.md for the phased rollout.
func buildAllocU8Body(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__fern_alloc"]
	var body []byte
	// base = __fern_alloc(n + 16) + 16
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
	// Zero the n payload bytes via memory.fill($base, 0, n). __fern_alloc may
	// reuse a freelist block carrying stale bytes; the interpreter returns a
	// zero-filled `u8[]`, so this backend must too (issue #2768) — read-before-
	// write callers (e.g. SHA padding) depend on it.
	body = inst.InstLocalGet(body, 1) // dst = $base
	body = inst.InstI32Const(body, 0) // value 0
	body = inst.InstLocalGet(body, 0) // n
	body = memory.InstMemoryFill(body)
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
	body = memory.InstMemoryCopy(body)
	return inst.PutFunctionBody(nil, inst.PutLocalsEmpty(nil), body)
}

// buildMemsetBody — (dst, b, n) → (). Emits `memory.fill`
// (0xFC 0x0B 0x00). b is treated as a byte (low 8 bits).
func buildMemsetBody(_ map[string]uint32) []byte {
	var body []byte
	body = inst.InstLocalGet(body, 0) // dst
	body = inst.InstLocalGet(body, 1) // b
	body = inst.InstLocalGet(body, 2) // n
	body = memory.InstMemoryFill(body)
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
	strLen := helperIdxs["__fern_str_len"]
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
// stride-1/8 variants. stride=4 is __arr_idx (buildArrIdxBody).
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
func buildArrIdx8Body(idxs map[string]uint32) []byte { return buildArrIdxStride(8)(idxs) }

// buildArrIdxStrideNC is buildArrIdxStride minus the two trap blocks — the
// bounds-check-elided (`_nc`) variant used when the caller has statically
// proven the index in range (the ForEach desugar's synthetic `iter[idx]`,
// #4380 lever 3). Just `base + i*stride`.
func buildArrIdxStrideNC(stride int32) func(map[string]uint32) []byte {
	return func(_ map[string]uint32) []byte {
		var body []byte
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

func buildArrIdxNCBody(idxs map[string]uint32) []byte  { return buildArrIdxStrideNC(4)(idxs) }
func buildArrIdx1NCBody(idxs map[string]uint32) []byte { return buildArrIdxStrideNC(1)(idxs) }
func buildArrIdx8NCBody(idxs map[string]uint32) []byte { return buildArrIdxStrideNC(8)(idxs) }

// buildStringFromBytesBody — (bs) → (data, len). bs is a u8[]
// heap pointer; length lives at [bs-4]. Output is the two-word
// string ABI:
//
//	bLen == 0:  (0, 0x80000000)               inline empty
//	bLen <= 7:  inline-packed (data, len)     no alloc
//	bLen >  7:  heap-form (out, bLen)         alloc + memory.copy
//
// Mirrors the WAT path's $string_from_bytes_unchecked structure.
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
	// rc=1-headered heap buffer (data = base+8) so an owned string local
	// reclaims it; the byte-copy loop writes to the returned data pointer.
	alloc := helperIdxs["__fern_alloc_rc1"]
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
	body = memory.InstMemoryCopy(body)
	// return ($out, $bLen)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 1)
	locals := inst.PutLocalsOneGroup(nil, 6, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStrCopyBody — (ptr, len) → (data, len). Copies `len` raw bytes
// at `ptr` into a FRESH owned two-word string. Used to turn a borrowed
// VIEW string — one whose data points into a shared buffer with no
// per-string rc header (argv_buf / environ, as args()/env() produce) —
// into a normal headered string, so it can flow through the rc system
// (inc/dec, container drops) exactly like a concat/slice result.
//
// Three-way result, identical to __fern_string_from_bytes / __str_slice:
//   - len == 0:  (0, 0x80000000)            inline empty (no alloc)
//   - len <= 7:  inline-packed (data, len)  no alloc
//   - len >  7:  (out, len), out = rc1-headered heap copy via memory.copy
//
// Locals (after the 2 params ptr=0, len=1):
//
//	2: $data (inline pack)
//	3: $len  (inline pack)
//	4: $out  (heap dst)
//	5: $i    (loop counter)
//	6: $byte
func buildStrCopyBody(helperIdxs map[string]uint32) []byte {
	alloc := helperIdxs["__fern_alloc_rc1"]
	var body []byte
	// if len == 0: return (0, 0x80000000)
	body = inst.InstLocalGet(body, 1)
	body = numeric.InstI32Eqz(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, 0)
	body = inst.InstI32Const(body, int32(-0x80000000))
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// if len <= 7: build inline-packed (data, len).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 7)
	body = numeric.InstI32LeU(body)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 2) // $data = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 3) // $len = 0
		body = inst.InstI32Const(body, 0)
		body = inst.InstLocalSet(body, 5) // $i = 0
		body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
		body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
		{
			// if $i >= len: break
			body = inst.InstLocalGet(body, 5)
			body = inst.InstLocalGet(body, 1)
			body = numeric.InstI32GeU(body)
			body = inst.InstBrIf(body, 1)
			// $byte = mem[ptr + i]
			body = inst.InstLocalGet(body, 0)
			body = inst.InstLocalGet(body, 5)
			body = numeric.InstI32Add(body)
			body = memory.InstI32Load8U(body, 0, 0)
			body = inst.InstLocalSet(body, 6) // $byte
			// pack: if i < 4: data |= byte << (i*8); else len |= byte << ((i-4)*8)
			body = inst.InstLocalGet(body, 5)
			body = inst.InstI32Const(body, 4)
			body = numeric.InstI32LtU(body)
			body = inst.InstIfStart(body, inst.BlocktypeEmpty)
			{
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
		// $len |= len << 24 | 0x80000000 (inline flag).
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
	// Heap form: $out = alloc_rc1(len); memory.copy($out, ptr, len).
	body = inst.InstLocalGet(body, 1)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 4) // $out
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstMemoryCopy(body)
	// return ($out, len)
	body = inst.InstLocalGet(body, 4)
	body = inst.InstLocalGet(body, 1)
	locals := inst.PutLocalsOneGroup(nil, 5, encode.ValtypeI32)
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
	strLen := helperIdxs["__fern_str_len"]
	strByte := helperIdxs["__fern_str_byte"]
	// rc=1-headered heap buffer (data = base+8) so an owned string local
	// reclaims it; the byte-copy loop writes to the returned data pointer.
	alloc := helperIdxs["__fern_alloc_rc1"]
	var body []byte
	// $src_len = __fern_str_len(base_data, base_len)
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
			// $byte = __fern_str_byte(base_data, base_len, low + i)
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
	body = memory.InstMemoryCopy(body)
	body = inst.InstLocalGet(body, 6)
	body = inst.InstLocalGet(body, 5)
	locals := inst.PutLocalsOneGroup(nil, 7, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReadLineBody — () → i32. Reads bytes from stdin via
// __fern_read_byte, accumulating into a growable u8 buffer
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
	// The accumulation buffer becomes the returned string's data (stored
	// directly into the Some box below), so header it with rc1 for
	// reclamation. Grown copies are also rc1 (abandoned intermediates
	// leak as before — harmless).
	alloc := helperIdxs["__fern_alloc_rc1"]
	allocBox := helperIdxs["__fern_alloc_box"]
	readByte := helperIdxs["__fern_read_byte"]
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
		// $byte = __fern_read_byte()
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
			body = memory.InstMemoryCopy(body)
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
	// Phase 1e-runtime: alloc_box prepends the static-sentinel rc
	// header so enum-ii's inc/dec no-op on this Option box.
	body = inst.InstI32Const(body, 4)
	body = inst.InstCall(body, allocBox)
	body = inst.InstLocalTee(body, 5)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 5)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// Build Some(line) box: alloc(16), tag=0, data, len. Phase
	// 1e-runtime: alloc_box prepends the static-sentinel rc header.
	body = inst.InstI32Const(body, 16)
	body = inst.InstCall(body, allocBox)
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
	alloc := idxs["__fern_alloc"]
	var body []byte
	// 12-byte Reader struct: rc sentinel @ +0, {fd} @ +8 — matching the
	// file Reader (buildOpenReaderBodyP2). The leading static rc sentinel
	// keeps __fern_retain / __fern_drop (which mutate mem[ptr-8]) off the
	// preceding static data segment — see issue #2550.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0)
	body = inst.InstI32Const(body, -0x80000000) // static rc sentinel
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 0) // data pointer = base + 8
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 0) // fd = 0 (stdin)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildStdinBodyP2 is the preview-2 variant: a Reader holds an
// input-stream handle (not an fd), so stdin's Reader stores the
// wasi:cli/stdin::get-stdin() handle. The preview-2 Reader methods
// (buildReaderReadLineFdBodyP2 etc.) blocking-read on it.
func buildStdinBodyP2(idxs map[string]uint32) []byte {
	alloc := idxs["__fern_alloc"]
	getStdin := idxs["wasi_get_stdin_p2"]
	var body []byte
	// 12-byte Reader struct: rc sentinel @ +0, {handle} @ +8 — matching
	// the file Reader (buildOpenReaderBodyP2). The leading static rc
	// sentinel keeps __fern_retain / __fern_drop (which mutate mem[ptr-8])
	// off the preceding static data segment — see issue #2550.
	body = inst.InstI32Const(body, 12)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalTee(body, 0)
	body = inst.InstI32Const(body, -0x80000000) // static rc sentinel
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 0) // data pointer = base + 8
	body = inst.InstLocalGet(body, 0)
	body = inst.InstCall(body, getStdin) // handle = get-stdin()
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 0)
	locals := inst.PutLocalsOneGroup(nil, 1, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildReaderReadLineBody — (r) → i32. Delegates to
// __fern_read_line, ignoring the receiver. Lives in the
// helper registry so __method_Reader_read_line's IR call
// site finds a real funcidx; once wasmbin grows TCP / file
// Readers, this dispatches on the receiver's discriminator.
func buildReaderReadLineBody(helperIdxs map[string]uint32) []byte {
	var body []byte
	body = inst.InstCall(body, helperIdxs["__fern_read_line"])
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
// Shares the wasi_args_* init path with __fern_arg_at via the
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
	alloc := helperIdxs["__fern_alloc"]
	strCopy := helperIdxs["__fern_str_copy"]
	argsSizes := helperIdxs["wasi_args_sizes_get"]
	argsGet := helperIdxs["wasi_args_get"]
	var body []byte
	// Lazy init: same shape as __fern_arg_at.
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
	// Build: result_raw = __fern_alloc(argc * 8 + 4)
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
		// Copy (cstr, len) into a fresh owned string (no view escapes):
		// ($cdata, $clen) = __fern_str_copy($cstr, $len). Stack returns
		// (data, len) → pop len into local 10, data into local 9.
		body = inst.InstLocalGet(body, 7)
		body = inst.InstLocalGet(body, 8)
		body = inst.InstCall(body, strCopy)
		body = inst.InstLocalSet(body, 10) // $clen
		body = inst.InstLocalSet(body, 9)  // $cdata
		// Store ($cdata, $clen) at result + i*8
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 9)
		body = memory.InstI32Store(body, 2, 0)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 6)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 10)
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
	locals := inst.PutLocalsOneGroup(nil, 11, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildArgsBodyP2 is the preview-2 variant of buildArgsBody —
// builds the length-prefixed string[] of argv from the
// wasi:cli/environment::get-arguments list<string> instead of
// preview-1 args_sizes_get / args_get. The canonical list elements
// are already (ptr, len) pairs, so each entry is a straight copy
// (no NUL walk). The built array is cached in the args_sizes
// scratch slots (argsSizesArgcAddr = result ptr, argsSizesBufAddr =
// built flag) — safe under preview-2 since the indexed helpers
// don't touch those slots.
//
// Locals: 0=$rb, 1=$argc, 2=$raw, 3=$result, 4=$list, 5=$i, 6=$el.
func buildArgsBodyP2(helperIdxs map[string]uint32) []byte {
	getArgs := helperIdxs["wasi_get_arguments_p2"]
	alloc := helperIdxs["__fern_alloc"]
	strCopy := helperIdxs["__fern_str_copy"]
	var body []byte
	body = appendArgsInitP2(body, getArgs, alloc, 0)
	// Built-array cache check: argsSizesBufAddr == 1 → return
	// the cached result ptr from argsSizesArgcAddr.
	body = inst.InstI32Const(body, argsSizesBufAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstIfStart(body, inst.BlocktypeEmpty)
	body = inst.InstI32Const(body, argsSizesArgcAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstReturn(body)
	body = inst.InstEnd(body)
	// $argc = mem[argsCountAddr]
	body = inst.InstI32Const(body, argsCountAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 1)
	// $raw = __fern_alloc($argc*8 + 4)
	body = inst.InstLocalGet(body, 1)
	body = inst.InstI32Const(body, 8)
	body = numeric.InstI32Mul(body)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstCall(body, alloc)
	body = inst.InstLocalSet(body, 2)
	// mem[$raw] = $argc (length prefix)
	body = inst.InstLocalGet(body, 2)
	body = inst.InstLocalGet(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	// $result = $raw + 4
	body = inst.InstLocalGet(body, 2)
	body = inst.InstI32Const(body, 4)
	body = numeric.InstI32Add(body)
	body = inst.InstLocalSet(body, 3)
	// $list = mem[argsPtrsAddr]
	body = inst.InstI32Const(body, argsPtrsAddr)
	body = memory.InstI32Load(body, 2, 0)
	body = inst.InstLocalSet(body, 4)
	// for i in 0..argc: copy (ptr, len) from $list+i*8 to $result+i*8
	body = inst.InstI32Const(body, 0)
	body = inst.InstLocalSet(body, 5)
	body = inst.InstBlockStart(body, inst.BlocktypeEmpty)
	body = inst.InstLoopStart(body, inst.BlocktypeEmpty)
	{
		body = inst.InstLocalGet(body, 5)
		body = inst.InstLocalGet(body, 1)
		body = numeric.InstI32GeU(body)
		body = inst.InstBrIf(body, 1)
		// $el = $list + i*8
		body = inst.InstLocalGet(body, 4)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 6)
		// Copy the (ptr, len) view at $el into a fresh owned string:
		// ($cdata, $clen) = __fern_str_copy(mem[$el], mem[$el+4]).
		body = inst.InstLocalGet(body, 6)
		body = memory.InstI32Load(body, 2, 0)
		body = inst.InstLocalGet(body, 6)
		body = memory.InstI32Load(body, 2, 4)
		body = inst.InstCall(body, strCopy)
		body = inst.InstLocalSet(body, 8) // $clen
		body = inst.InstLocalSet(body, 7) // $cdata
		// mem[$result + i*8] = $cdata
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 7)
		body = memory.InstI32Store(body, 2, 0)
		// mem[$result + i*8 + 4] = $clen
		body = inst.InstLocalGet(body, 3)
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 8)
		body = numeric.InstI32Mul(body)
		body = numeric.InstI32Add(body)
		body = inst.InstI32Const(body, 4)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalGet(body, 8)
		body = memory.InstI32Store(body, 2, 0)
		// $i++
		body = inst.InstLocalGet(body, 5)
		body = inst.InstI32Const(body, 1)
		body = numeric.InstI32Add(body)
		body = inst.InstLocalSet(body, 5)
		body = inst.InstBr(body, 0)
	}
	body = inst.InstEnd(body)
	body = inst.InstEnd(body)
	// Cache: argsSizesArgcAddr = $result, argsSizesBufAddr = 1
	body = inst.InstI32Const(body, argsSizesArgcAddr)
	body = inst.InstLocalGet(body, 3)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstI32Const(body, argsSizesBufAddr)
	body = inst.InstI32Const(body, 1)
	body = memory.InstI32Store(body, 2, 0)
	body = inst.InstLocalGet(body, 3)
	locals := inst.PutLocalsOneGroup(nil, 9, encode.ValtypeI32)
	return inst.PutFunctionBody(nil, locals, body)
}

// buildSqrtF64Body — (f64) → f64. Thin wrapper around the
// wasm-native f64.sqrt instruction. Exposed via the source-
// language method `(x: f64) sqrt()` in std/float.fern which
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
