// The ownership signatures of the reference-count helpers.
//
// A defined function's ownership is a property of its body, so the
// interprocedural fixpoint #7786 wants can derive it. The runtime
// helpers have no body in the IR: their reference-count behaviour is
// written in the backends, and every analysis over the op stream has to
// be told. This table is that record.
//
// It is a SECOND record rather than a shared one, for the same reason
// `verifyprovided.go` keeps its own arity table: a verifier that reads
// its facts out of the emitter agrees with the compiler by
// construction. TestRcSigsCoverEveryRuntimeHelper compares this file's
// three buckets against the wasm backend's helper registry and fails on
// a name that is in none of them, so a new helper cannot arrive
// unclassified.
//
// # Why a name rule alone does not work
//
// The obvious shortcut — treat anything called `*_dec` / `*_drop` /
// `*_free` as a release — is wrong in both directions, measured over the
// conformance corpus:
//
//   - `mk_free` and `map_inc` are ordinary user functions, and
//     `hex_decode` and `is_non_decreasing` match a "dec" substring.
//     Applied over the corpus that rule finds 259 names, where the
//     answer is 17 runtime helpers and one closed family of generated
//     drops.
//   - `__method_<T>_drop` looks like the user `Drop` glue and sometimes
//     is, but `userDropFnName` resolves a `Drop` impl through
//     `info.ResolveMethod`, so the finalizer can carry ANY name; and a
//     user method called `foo_drop` mangles to a name with the same
//     suffix. User drops are defined functions and belong to the
//     fixpoint, not here.
//   - `std/async.fern` defines `__drop_losers(drv, fs, winner)`. It
//     matches `__drop_` and releases none of its three parameters. Only
//     the closed prefix list below excludes it.
//
// # What a signature says, and what it does not
//
// One axis: what the call does to the caller's ownership unit on ONE
// operand, named by index. The other axis — whether the RESULT carries
// a unit — is not modelled. Nothing asking today needs it, and half the
// registry allocates, so writing it down without a use would be 147
// unchecked claims.
package ir

import "strings"

// RcEffect is what a call does to the caller's ownership unit on its
// counted operand.
type RcEffect int

const (
	// RcRetain adds a unit. The caller keeps its own.
	RcRetain RcEffect = iota
	// RcRelease consumes the caller's unit. The helper may or may not
	// reclaim — `__fern_rc_dec` frees only at the last reference — but
	// either way the caller no longer holds one.
	RcRelease
	// RcInspect reads the count and transfers nothing.
	RcInspect
	// RcMove consumes the caller's unit on the operand and hands back
	// an owned result, which may or may not be the same address. The
	// copy-on-write helpers are the family: unique receivers come back
	// unchanged, shared ones are released and replaced.
	RcMove
)

// RcSig is one helper's effect and which argument it applies to.
type RcSig struct {
	// Operand is the argument index of the counted pointer.
	//
	// Every entry below is 0, and that is a measured fact rather than
	// an assumption the type encodes: the field exists because the
	// stdlib's own map helpers are not — `__map_dec_value(buf, v)` and
	// `__map_free_val_cell(buf, v)` release argument 1 and borrow
	// argument 0. Those are defined Fern functions in
	// `internal/stdlib/core/map.fern`, so the fixpoint derives them and
	// they are deliberately absent here; a runtime helper of that shape
	// would need the index.
	Operand int
	Effect  RcEffect

	// ResultIsOperand is true when the call hands back the pointer it
	// was given, so the result denotes the same object under a new
	// name.
	//
	// Almost every retain and release does — the helpers return their
	// argument so an inc or a dec can be spliced into an expression
	// chain without a temporary — and an analysis that does not follow
	// the alias reports the wrong answer, because code after the call
	// reads the RESULT. False for `__free`, which returns nothing; for
	// `__fern_rc_is_unique`, which returns a boolean; and for the moves,
	// whose result is a DIFFERENT object whenever the receiver was
	// shared.
	ResultIsOperand bool
}

// rcRuntimeSigs is every runtime helper that moves a reference count,
// with its effect read off the helper's own definition in
// `internal/codegen/wasmbin/runtime.go`.
var rcRuntimeSigs = map[string]RcSig{
	"__fern_rc_inc":  {0, RcRetain, true},
	"__fern_str_inc": {0, RcRetain, true},

	"__fern_rc_dec":       {0, RcRelease, true},
	"__fern_str_dec":      {0, RcRelease, true},
	"__fern_arr_dec":      {0, RcRelease, true},
	"__fern_box_free":     {0, RcRelease, true},
	"__fern_cell_free":    {0, RcRelease, true},
	"__fern_map_drop":     {0, RcRelease, true},
	"__fern_closure_drop": {0, RcRelease, true},
	"__fern_drop_arr_ptr": {0, RcRelease, true},
	"__fern_drop_arr_str": {0, RcRelease, true},
	// (base, size) with no count to read and no result: an
	// unconditional return to the freelist. The caller's unit is gone
	// either way.
	"__free": {0, RcRelease, false},

	"__fern_rc_is_unique": {0, RcInspect, false},

	"__fern_arr_cow_inplace":     {0, RcMove, false},
	"__fern_arr_cow_inplace_ptr": {0, RcMove, false},
	"__fern_arr_cow_inplace_str": {0, RcMove, false},
	// "CONSUMES a", per its own definition; b is borrowed.
	"__fern_str_append": {0, RcMove, false},
}

// rcUnmodelled are helpers that do move counts, and whose movement one
// operand effect cannot express. They report no signature — the same
// answer as an unknown callee — so a count derived from this table
// reads LOW rather than wrong.
var rcUnmodelled = map[string]string{
	// Two paths with opposite effects on the receiver, chosen at
	// runtime: unique-with-capacity bumps arr's rc to 2 and returns
	// arr, while the grow path allocates and leaves the old buffer for
	// the caller's own __fern_arr_dec. Neither retain nor move
	// describes the pair.
	"__fern_arr_push_grow":          "in-place path retains, grow path leaves the release to the caller",
	"__fern_arr_push_grow_ptr":      "as __fern_arr_push_grow, plus a per-element retain on the copy",
	"__fern_arr_push_grow_str":      "as __fern_arr_push_grow, plus a per-element retain on the copy",
	"__fern_arr_push_grow_move_ptr": "as __fern_arr_push_grow, with the element retain gated on rc != 1",
	"__fern_arr_push_grow_move_str": "as __fern_arr_push_grow, with the element retain gated on rc != 1",
	// The token is a block that has already stopped being a counted
	// reference — the donor was released to produce it. The protocol
	// around it is what verifyrc.go checks.
	"__alloc_reuse": "consumes a reuse token, which is a raw block rather than a counted reference",
}

// rcInert are the runtime helpers that move no reference count on any
// operand. Most borrow and compute; the allocators produce an owned
// result from a size, which is the result axis this table does not
// model. The fd-closing and host-handle helpers (`__fern_tcp_close`,
// `__fern_wasm_pollable_drop`) release an OS resource rather than a
// Fern object, which is not the same thing and is not counted.
//
// The list is written out rather than derived so that a helper added to
// the runtime fails TestRcSigsCoverEveryRuntimeHelper until someone
// decides which bucket it belongs in.
var rcInert = map[string]bool{
	// Arithmetic, libc shims and the canonical-ABI allocator. cabi_realloc
	// moves host memory for the component ABI, which carries no Fern
	// reference count.
	"__fern_idiv_s32": true, "__fern_idiv_s64": true, "__fern_idiv_u32": true,
	"__fern_idiv_u64": true, "__fern_irem_s32": true, "__fern_irem_s64": true,
	"__fern_irem_u32": true, "__fern_irem_u64": true,
	"cabi_realloc": true, "isatty": true, "poll": true,

	"__alloc": true, "__alloc_u8": true, "__arr_idx": true,
	"__arr_idx_1": true, "__arr_idx_1_nc": true, "__arr_idx_8": true,
	"__arr_idx_8_nc": true, "__arr_idx_nc": true, "__build_io_error": true,
	"__bytes_to_lang_string": true, "__fern_abs_f64": true,
	"__fern_alloc": true, "__fern_alloc_box": true, "__fern_alloc_rc1": true,
	"__fern_arg_at": true, "__fern_arg_count": true, "__fern_args": true,
	"__fern_arr_push_shared_bytes": true,
	"__fern_arr_push_shared_count": true, "__fern_ascii_run": true,
	"__fern_ceil_f64": true, "__fern_cos_f64": true,
	"__fern_create_dir_all": true, "__fern_env": true, "__fern_env_at": true,
	"__fern_env_count": true, "__fern_eprint": true, "__fern_exit": true,
	"__fern_exp_f64": true, "__fern_floor_f64": true,
	"__fern_heap_bump_bytes": true, "__fern_log_f64": true,
	"__fern_map_hash_seed": true, "__fern_memchr": true,
	"__fern_monotonic_ns": true, "__fern_now_ns": true,
	"__fern_now_unix_ms": true, "__fern_open_appender": true,
	"__fern_open_dir": true, "__fern_open_reader": true,
	"__fern_open_writer": true, "__fern_pow_f64": true, "__fern_print": true,
	"__fern_putchar": true, "__fern_random_bytes": true,
	"__fern_random_i32": true, "__fern_rc_underflow_count": true,
	"__fern_read_byte": true, "__fern_read_dir": true,
	"__fern_read_dir_raw": true, "__fern_read_file": true,
	"__fern_read_file_bytes": true, "__fern_read_line": true,
	"__fern_reader_close": true, "__fern_reader_close_fd": true,
	"__fern_reader_read_chunk": true, "__fern_reader_read_line": true,
	"__fern_reader_read_line_fd": true, "__fern_remove_dir_all": true,
	"__fern_remove_file": true, "__fern_rmdir_rec": true,
	"__fern_sin_f64": true, "__fern_sqrt_f64": true, "__fern_stat": true,
	"__fern_stderr": true, "__fern_stdin": true, "__fern_stdout": true,
	"__fern_str_byte": true, "__fern_str_copy": true, "__fern_str_len": true,
	"__fern_string_from_bytes": true, "__fern_tcp_accept": true,
	"__fern_tcp_close": true, "__fern_tcp_connect": true,
	"__fern_tcp_listen": true, "__fern_tcp_pollable": true,
	"__fern_tcp_recv": true, "__fern_tcp_send": true, "__fern_temp_dir": true,
	"__fern_trunc_f64": true, "__fern_udp_send": true,
	"__fern_utf8_valid": true, "__fern_wasm_block": true,
	"__fern_wasm_poll": true, "__fern_wasm_pollable_drop": true,
	"__fern_wasm_timer_pollable": true, "__fern_write": true,
	"__fern_write_file": true, "__fern_writer_close": true,
	"__fern_writer_write": true, "__http_entry": true, "__load_i32": true,
	"__load_i64": true, "__load_ptr": true, "__memcpy": true,
	"__memset": true, "__method_string_as_bytes": true,
	"__network_handle": true, "__ptr_width": true, "__slice_idx": true,
	"__slice_idx_1": true, "__slice_idx_4": true, "__slice_idx_8": true,
	"__slice_make": true, "__slice_range": true, "__store_i32": true,
	"__store_i64": true, "__store_ptr": true, "__str_concat": true,
	"__str_eq": true, "__str_idx": true, "__str_ord": true,
	"__str_slice": true,
}

// RcHelperClassified reports whether name is a runtime helper this file
// has classified — with a signature, as moving counts in a shape one
// operand effect cannot express, or as moving none.
//
// It exists for the wasm-registry completeness test; analyses want
// RcHelperSig.
func RcHelperClassified(name string) bool {
	if _, ok := rcRuntimeSigs[name]; ok {
		return true
	}
	if _, ok := rcUnmodelled[name]; ok {
		return true
	}
	return rcInert[name]
}

// RcClassifiedRuntimeNames lists every runtime helper this file names,
// so the completeness test can also catch an entry for a helper that no
// longer exists.
func RcClassifiedRuntimeNames() []string {
	out := make([]string, 0, len(rcRuntimeSigs)+len(rcUnmodelled)+len(rcInert))
	for n := range rcRuntimeSigs {
		out = append(out, n)
	}
	for n := range rcUnmodelled {
		out = append(out, n)
	}
	for n := range rcInert {
		out = append(out, n)
	}
	return out
}

// generatedDropPrefixes are the per-type drop functions lowering
// generates. Every one takes the value it drops as argument 0 and
// releases it.
//
// Arity is NOT uniform and so is not part of the rule: `__drop_dyn_<set>`
// takes two arguments on wasm, where a `dyn` value is an inline
// (data, vtable) pair, and one on the natives, where it is boxed.
//
// The list is closed on purpose. `__drop_` alone would swallow
// `std/async.fern`'s `__drop_losers`, which releases none of its
// arguments.
var generatedDropPrefixes = []string{
	"__drop_struct_flat_",
	"__drop_struct_",
	"__drop_enum_",
	"__drop_tuple_",
	"__drop_dyn_",
	"__drop_arr_struct_",
	"__drop_arr_tuple_",
	"__drop_arr_enum_",
	"__drop_arr_dyn_",
	"__drop_arr_arr_",
	"__drop_arr_of_",
	"__drop_map_",
}

// generatedDropNames are the generated drops whose name carries no type
// suffix, so no prefix rule reaches them.
var generatedDropNames = map[string]bool{
	"__drop_arr_closure":   true,
	"__drop_closure_value": true,
	"__map_drop_values":    true,
}

// RcHelperSig reports what a call to name does to the caller's
// ownership unit, and whether anything is known about it at all.
//
// ok is false for a defined function — whose ownership is the
// fixpoint's job — and for the helpers in rcUnmodelled.
func RcHelperSig(name string) (RcSig, bool) {
	if s, ok := rcRuntimeSigs[name]; ok {
		return s, true
	}
	if generatedDropNames[name] {
		return RcSig{0, RcRelease, true}, true
	}
	for _, p := range generatedDropPrefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return RcSig{0, RcRelease, true}, true
		}
	}
	return RcSig{}, false
}

// RcHelperUnmodelled reports whether name is a runtime helper this file
// records as moving reference counts in a shape one operand effect
// cannot express, and why.
//
// A caller that treats "no signature" as "no effect" is wrong about
// exactly these names, and right about the inert ones. Asking lets it
// tell the two apart and count the gap instead of absorbing it.
func RcHelperUnmodelled(name string) (reason string, ok bool) {
	r, ok := rcUnmodelled[name]
	return r, ok
}

// RcReleaseNames lists every runtime helper whose signature says a call
// gives up the caller's unit on its operand, and RcGeneratedDropPrefixes
// / RcGeneratedDropNames expose the rule that covers the generated
// drops.
//
// They exist so the self-host mirror in
// `examples/self_host/irverifyrc.fern` can be pinned entry-for-entry
// against this file rather than against a regexp over it.
func RcReleaseNames() []string {
	var out []string
	for n := range rcRuntimeSigs {
		if _, ok := RcReleases(n); ok {
			out = append(out, n)
		}
	}
	return out
}

func RcGeneratedDropPrefixes() []string {
	return append([]string(nil), generatedDropPrefixes...)
}

func RcGeneratedDropNames() []string {
	out := make([]string, 0, len(generatedDropNames))
	for n := range generatedDropNames {
		out = append(out, n)
	}
	return out
}

// RcReleases reports whether a call to name gives up the caller's unit
// on its counted operand — the question `verifyrc.go` and
// `internal/ssa` both ask. RcMove counts: the operand's unit is gone,
// and what comes back is a different unit on the result.
func RcReleases(name string) (operand int, ok bool) {
	s, ok := RcHelperSig(name)
	if !ok || (s.Effect != RcRelease && s.Effect != RcMove) {
		return 0, false
	}
	return s.Operand, true
}

// RcRetains reports whether a call to name adds a unit on its counted
// operand.
func RcRetains(name string) (operand int, ok bool) {
	s, ok := RcHelperSig(name)
	if !ok || s.Effect != RcRetain {
		return 0, false
	}
	return s.Operand, true
}
