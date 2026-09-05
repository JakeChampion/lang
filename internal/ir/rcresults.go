// The RESULT axis of the ownership signature table (#7786).
//
// `rcsigs.go` answers what a callee does to its ARGUMENTS and says in
// its own header that "whether the RESULT carries a unit is not
// modelled". This is that half. #7786's definition of the table asks
// for both — "which of its refcounted parameters are consumed (owned)
// versus borrowed, and whether its return carries an ownership unit".
//
// # Why it is five answers and not a boolean
//
// A boolean "does the caller own the result" merges facts that behave
// differently when the caller acts on them, and the difference is not
// cosmetic — it decides whether an emitted release is correct, inert,
// or memory corruption:
//
//   - a LIVE rc=1 header: the release reclaims;
//   - a static-sentinel header (0x80000000): every rc helper
//     short-circuits on the high bit, so the release is a no-op and the
//     block is never reclaimed at all;
//   - no header: the release reads whatever bytes precede the block,
//     which is a neighbouring object's payload.
//
// Nineteen of the twenty-one Result / Option / IoError-returning
// helpers are in the second class. Calling them "owned" would emit a
// dec at every call site that cannot possibly reclaim.
//
// # Read the body, not the name
//
// Every entry here was read off the helper's definition in
// `internal/codegen/wasmbin/runtime.go` and its siblings, which
// `rcsigs.go` names as the canonical one. The names lie often enough
// that this is the whole method:
//
//   - `__alloc_u8` sounds like the raw allocator beside it and is not:
//     it lays down cap@-12, rc=1@-8, len@-4.
//   - `__fern_read_dir_raw` puts a used-byte count at buf-4, which is
//     where `__alloc_u8` puts its length — and it is NOT that layout.
//     It is `__fern_alloc(cap+4)` with a hand-written prefix and no rc
//     word anywhere.
//   - `__fern_arg_at` and `__fern_env_at` have the same signature and
//     opposite answers. `env_at` copies through `__fern_str_copy` and
//     hands back an owned string; `arg_at` returns a view straight into
//     `argv_buf`. `__fern_str_copy`'s own doc names that view as the
//     thing it exists to convert.
package ir

import "github.com/jakechampion/lang/internal/ast"

// RcResult is what a call's returned pointer means for the caller's
// ownership books.
type RcResult int

const (
	// RcResultNone: nothing reference counting applies to. A void
	// return, a float, an integer count, a file descriptor, a boolean,
	// or an opaque host handle that is not a memory address.
	RcResultNone RcResult = iota

	// RcResultOwned: a freshly allocated object with a LIVE rc=1
	// header. The caller holds a unit and must release it.
	RcResultOwned

	// RcResultImmortal: freshly allocated, pointer-shaped, and carrying
	// the static-sentinel rc header. The caller holds NO unit: a
	// release short-circuits on the high bit and reclaims nothing.
	//
	// Distinguished from RcResultNone because the value IS an address —
	// it can be stored, loaded and passed on — and from RcResultOwned
	// because releasing it does nothing. A leak walk must not report
	// one as held; a reclamation pass must not expect one to be freed.
	RcResultImmortal

	// RcResultRaw: freshly allocated arena memory with no rc header at
	// all. The caller holds no unit, and unlike an immortal block a
	// release here is not inert — `[ptr-8]` is a neighbouring object's
	// bytes.
	RcResultRaw

	// RcResultBorrow: an address into something the caller already
	// holds, or into static or host memory. An element address, a slice
	// header's target, a pointer read out of memory, an argv view.
	RcResultBorrow

	// RcResultOperand: the pointer the call was handed, under a new
	// name. What the caller still owns is the ARGUMENT axis's answer —
	// `RcArg.ResultIsOperand` already carries the aliasing, and the
	// effect beside it says whether a unit survived.
	//
	// It is a bucket here rather than an absence because the two halves
	// of that family disagree: `__fern_rc_inc` hands back a pointer the
	// caller still owns and `__fern_box_free` hands back one whose
	// memory is gone. A result axis that read `ResultIsOperand` as
	// "owned" would get all eleven release helpers wrong.
	RcResultOperand
)

func (r RcResult) String() string {
	switch r {
	case RcResultOwned:
		return "owned"
	case RcResultImmortal:
		return "immortal"
	case RcResultRaw:
		return "raw"
	case RcResultBorrow:
		return "borrow"
	case RcResultOperand:
		return "operand"
	default:
		return "none"
	}
}

// rcResultOwned: a live rc=1 header the caller must release.
var rcResultOwned = map[string]bool{
	// The counted allocators themselves.
	"__fern_alloc_rc1": true, // rc=1 at base+0, payload size at base+4
	"__alloc_u8":       true, // cap@-12, rc=1@-8, len@-4, payload zeroed

	// String production. Each is three-way — empty, inline-packed (<=7
	// bytes), or an rc1 heap copy — and "owned" is the right answer for
	// all three: a release of an inline or empty string short-circuits
	// on the inline bit, so the caller may always release the result.
	"__str_concat":             true,
	"__str_slice":              true,
	"__fern_str_copy":          true,
	"__fern_string_from_bytes": true,
	"__bytes_to_lang_string":   true,
	// CONSUMES its receiver and hands back one owned string, in place
	// when unique and freshly concatenated otherwise. Either path
	// leaves the caller holding exactly one unit.
	"__fern_str_append": true,
	// Copies through __fern_str_copy — unlike __fern_arg_at beside it.
	"__fern_env_at": true,
	// Snapshots the string builder into a fresh rc=1 string and rewinds
	// it, so the result aliases nothing the builder goes on to overwrite.
	"strbuf_take": true,

	// Byte buffers in the __alloc_u8 box shape.
	"__fern_random_bytes": true,
	"__fern_tcp_recv":     true,

	// Slice view headers: an rc1 block `{data_ptr, len}` from
	// __fern_alloc_rc1, released as a header alone — the bytes it views
	// belong to the array or string it was cut from. `as_bytes` on an
	// inline-packed string first copies the bytes into a bare __fern_alloc
	// block the header points at; that copy has no owner (see the
	// backends' as_bytes helpers), the header is still the caller's unit.
	"__slice_make":             true,
	"__method_string_as_bytes": true,
}

// rcResultImmortal: fresh, pointer-shaped, static-sentinel header. The
// caller holds nothing and a release reclaims nothing.
//
// `__fern_alloc_box` plus the closed set of its callers, which
// `runtime.go`'s own `helperAllocBoxCallers` maintains — this list is
// checked against it rather than kept in step by hand.
var rcResultImmortal = map[string]bool{
	"__fern_alloc_box": true,

	"__fern_env":                 true,
	"__fern_read_line":           true,
	"__fern_reader_read_line":    true, // delegates to __fern_read_line
	"__build_io_error":           true,
	"__fern_read_file":           true,
	"__fern_read_file_bytes":     true,
	"__fern_write_file":          true,
	"__fern_open_reader":         true,
	"__fern_open_writer":         true,
	"__fern_open_appender":       true,
	"__fern_reader_close_fd":     true,
	"__fern_writer_close":        true,
	"__fern_writer_write":        true,
	"__fern_reader_read_line_fd": true,
	"__fern_reader_read_chunk":   true,
	"__fern_remove_file":         true,
	"__fern_stat":                true,
	"__fern_lstat":               true,
	"__fern_read_dir":            true,
	"__fern_remove_dir_all":      true,
	"__fern_temp_dir":            true,
	"__fern_create_dir_all":      true,

	// Sentinel-headered Writer / Reader structs.
	"__fern_stdout": true,
	"__fern_stderr": true,
	"__fern_stdin":  true,
}

// rcOwnedPayloadBuiltins names the builtins — by the callee spelling the IR
// sees, which every backend maps to the runtime helper in the comment —
// whose rcResultImmortal Option / Result box carries a SUCCESS payload the
// caller owns: a fresh rc=1 string (`__fern_alloc_rc1`) or u8[]
// (`__alloc_u8`) built for this call, on every backend. The box needs no
// release; the payload's unit is the caller's, and a match that binds it
// takes ownership (computeConsumingOwnedMatches → consumingBindings). A
// failure payload (`IoError`) is an immortal box and is not owned.
var rcOwnedPayloadBuiltins = map[string]bool{
	"__method_Reader_read_chunk": true, // __fern_reader_read_chunk
	"__method_Reader_read_line":  true, // __fern_reader_read_line
	"read_line":                  true, // __fern_read_line
	"env":                        true, // __fern_env
	"read_file":                  true, // __fern_read_file
	"read_file_bytes":            true, // __fern_read_file_bytes
}

// ownedPayloadType reports whether a binding of type `t` extracted from an
// rcOwnedPayloadBuiltins box is the owned payload — the string / byte-array
// shapes those helpers build fresh — rather than an immortal failure box.
func ownedPayloadType(t ast.Type) bool {
	switch t.(type) {
	case ast.StringType, ast.ArrayType:
		return true
	}
	return false
}

// rcResultRaw: fresh arena memory with no rc header.
//
// Several of these are bimodal in a way that does not change the
// answer: a TCP helper returns either a struct pointer or a negative
// errno, and neither carries a unit.
var rcResultRaw = map[string]bool{
	"__fern_alloc": true, // the bare bump cursor
	"__alloc":      true, // a one-instruction forwarder to it
	"cabi_realloc": true,

	// A dirent buffer with a hand-written used-count at buf-4. NOT the
	// __alloc_u8 layout despite the same offset — see this file's
	// header.
	"__fern_read_dir_raw": true,

	// 12-byte socket structs, or -errno.
	"__fern_tcp_listen":  true,
	"__fern_tcp_accept":  true,
	"__fern_tcp_connect": true,
}

// rcResultBorrow: an address into memory the caller already holds, or
// into static or host memory.
var rcResultBorrow = map[string]bool{
	// Element addresses. The container owns the storage.
	"__arr_idx":      true,
	"__arr_idx_1":    true,
	"__arr_idx_8":    true,
	"__arr_idx_nc":   true,
	"__arr_idx_1_nc": true,
	"__arr_idx_8_nc": true,
	"__slice_idx":    true,
	"__slice_idx_1":  true,
	"__slice_idx_4":  true,
	"__slice_idx_8":  true,
	// For a heap string this is base_data + i. For an inline string it
	// spills to a fixed scratch region and returns an address into
	// THAT, which is borrowed from neither operand — but it is not
	// owned either, and nothing may release it.
	"__str_idx": true,

	// A pointer read out of memory: reachable from the container rather
	// than identical to it. `ssa.UnitsOf` and `ownership_returns.go`
	// reach the same conclusion for the OpLoad they lift this to.
	"__load_ptr": true,

	// A view straight into argv_buf, with no per-string rc header.
	"__fern_arg_at": true,

	// The census itoa hands back the caller's own write cursor,
	// advanced past the digits it wrote.
	"__fern_lc_wrnum": true,
}

// rcResultOperand: the argument, renamed. See RcResultOperand.
var rcResultOperand = map[string]bool{
	"__fern_rc_inc":       true,
	"__fern_rc_dec":       true,
	"__fern_str_inc":      true,
	"__fern_str_dec":      true,
	"__fern_arr_dec":      true,
	"__fern_box_free":     true,
	"__fern_cell_free":    true,
	"__fern_map_drop":     true,
	"__fern_closure_drop": true,
	"__fern_drop_arr_ptr": true,
	"__fern_drop_arr_str": true,
}

// rcResultUnmodelled names the helpers whose result cannot be given one
// answer, with the reason.
//
// This is the same stance `rcUnmodelled` takes on the argument axis and
// for the same reason: a count derived from this table reads LOW rather
// than wrong, and a caller can report the gap instead of absorbing it.
var rcResultUnmodelled = map[string]string{
	// Bimodal by construction: the in-place path returns the operand
	// and the grow path returns a fresh buffer. Already unmodelled on
	// the argument axis for the same reason.
	"__fern_arr_push_grow":          "in-place returns the receiver, the grow path a fresh buffer",
	"__fern_arr_push_grow_ptr":      "in-place returns the receiver, the grow path a fresh buffer",
	"__fern_arr_push_grow_str":      "in-place returns the receiver, the grow path a fresh buffer",
	"__fern_arr_push_grow_move_ptr": "in-place returns the receiver, the grow path a fresh buffer",
	"__fern_arr_push_grow_move_str": "in-place returns the receiver, the grow path a fresh buffer",
	"__fern_arr_cow_inplace":        "rc==1 returns the receiver, rc>1 a fresh copy",
	"__fern_arr_cow_inplace_ptr":    "rc==1 returns the receiver, rc>1 a fresh copy",
	"__fern_arr_cow_inplace_str":    "rc==1 returns the receiver, rc>1 a fresh copy",
	"__alloc_reuse":                 "returns the donor token on a size-class match, a fresh raw block otherwise",

	// Fresh on the first call and the cached pointer on every later
	// one. One name, two answers, and which one a given call site gets
	// is not a static property.
	"__fern_args": "built once and cached; the first call is fresh and the rest are borrows",
}

// rcResultNonPointer: the result is not an address reference counting
// can apply to.
//
// Three mechanical groups plus one that needs reading. The void, f64
// and i64 returns cannot be a wasm32 pointer at all — a pointer there
// is an i32 — so those are settled by the registry's own result
// valtype. The i32 group is the one a shape check cannot settle:
// `verifyprovided.go` files a heap pointer and a byte count as the same
// `rWord`, so each of these was decided from what the helper returns.
var rcResultNonPointer = map[string]bool{
	// Void.
	"__fern_print": true, "__fern_eprint": true, "__fern_write": true,
	"__fern_putchar": true, "__fern_exit": true, "__free": true,
	"__fern_lc_report": true,
	"__memcpy":         true, "__memset": true, "__store_i32": true,
	"__store_i64": true, "__store_ptr": true, "__http_entry": true,
	"__fern_reader_close": true, "__fern_sleep_ms": true,
	"strbuf_reset": true, "strbuf_append": true,

	// f64.
	"__fern_abs_f64": true, "__fern_ceil_f64": true, "__fern_cos_f64": true,
	"__fern_exp_f64": true, "__fern_floor_f64": true, "__fern_log_f64": true,
	"__fern_pow_f64": true, "__fern_round_f64": true, "__fern_sin_f64": true,
	"__fern_sqrt_f64": true, "__fern_trunc_f64": true,

	// i64.
	"__fern_arr_push_shared_bytes": true, "__fern_heap_bump_bytes": true,
	"__fern_idiv_s64": true, "__fern_idiv_u64": true, "__fern_irem_s64": true,
	"__fern_irem_u64": true, "__fern_monotonic_ns": true, "__fern_now_ns": true,
	"__fern_now_unix_ms": true, "__load_i64": true,

	// i32 counts, indices, comparisons and booleans.
	"__fern_str_len": true, "__fern_str_byte": true, "__fern_memchr": true,
	"__fern_ascii_run": true, "__fern_rmemchr": true,
	"__fern_count_byte": true,
	"__str_eq":          true,
	"__str_ord":         true, "__fern_env_count": true, "__fern_arg_count": true,
	"__fern_read_byte": true, "__fern_random_i32": true,
	"__fern_map_hash_seed": true, "__load_i32": true, "__load_u8": true,
	"__ptr_width":   true,
	"__slice_range": true, "__fern_idiv_s32": true, "__fern_idiv_u32": true,
	"__fern_irem_s32": true, "__fern_irem_u32": true, "isatty": true,
	"__wasi_errno_of_code": true,

	// The rc probes and the uniqueness test — counters and a boolean.
	"__fern_rc_is_unique": true, "__fern_rc_underflow_count": true,
	"__fern_arr_push_shared_count": true,

	// File descriptors and errnos. `__fern_open_dir` answers an fd or
	// -(errno) and `__fern_rmdir_rec` an errno; neither is an address.
	"__fern_open_dir": true, "__fern_rmdir_rec": true,

	// Byte counts and status codes from the socket layer.
	"__fern_tcp_send": true, "__fern_udp_send": true, "__fern_tcp_close": true,

	// Opaque host handles. Pointer-SHAPED and not memory: a wasi
	// pollable or network handle indexes a host table. `rcsigs.go`
	// draws the same line on the argument axis, where these helpers
	// "release an OS resource rather than a Fern object".
	"__fern_wasm_timer_pollable": true, "__fern_wasm_block": true,
	"__fern_wasm_poll": true, "__fern_wasm_pollable_drop": true,
	"__network_handle": true, "__fern_tcp_pollable": true, "poll": true,
}

// RcHelperResult reports what a callee's returned pointer means for the
// caller's ownership books.
//
// ok is false for a function the IR defines — that is the
// interprocedural fixpoint's answer, not a table's — and for a helper
// `rcResultUnmodelled` records as having no single answer.
func RcHelperResult(name string) (RcResult, bool) {
	if resolved, ok := builtinRuntimeAlias(name); ok {
		name = resolved
	}
	if _, unmodelled := rcResultUnmodelled[name]; unmodelled {
		return RcResultNone, false
	}
	switch {
	case rcResultOwned[name]:
		return RcResultOwned, true
	case rcResultImmortal[name]:
		return RcResultImmortal, true
	case rcResultRaw[name]:
		return RcResultRaw, true
	case rcResultBorrow[name]:
		return RcResultBorrow, true
	case rcResultOperand[name]:
		return RcResultOperand, true
	}
	// A generated per-type drop hands back the pointer it was given,
	// exactly like the hand-written drops above.
	if isGeneratedDrop(name) {
		return RcResultOperand, true
	}
	if rcResultNonPointer[name] {
		return RcResultNone, true
	}
	return RcResultNone, false
}

// RcHelperResultClassified reports whether the name has a decision on
// the result axis at all — the total predicate the completeness gates
// enumerate against, the twin of RcHelperClassified.
func RcHelperResultClassified(name string) bool {
	if resolved, ok := builtinRuntimeAlias(name); ok {
		name = resolved
	}
	if _, ok := rcResultUnmodelled[name]; ok {
		return true
	}
	if rcResultOwned[name] || rcResultImmortal[name] || rcResultRaw[name] ||
		rcResultBorrow[name] || rcResultOperand[name] || rcResultNonPointer[name] {
		return true
	}
	return isGeneratedDrop(name)
}
