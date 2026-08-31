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

// RcArg is what a call does to the caller's ownership unit on ONE
// argument.
type RcArg struct {
	// Index is the argument's position.
	Index  int
	Effect RcEffect

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

// RcSig is a callee's effect on each argument that carries an ownership
// unit. An argument not listed is borrowed.
//
// It is a LIST rather than one operand, and the reason is measured
// rather than anticipated: `__method_Map_set(m, k, v)` consumes both
// the key and the value. Every runtime helper happens to move a count
// on exactly one argument, so a single-operand shape fitted them and
// then could not say what the very first builtin classified needed.
//
// The index matters too, and not every counted argument is argument 0:
// `__map_dec_value(buf, v)` and `__map_free_val_cell(buf, v)` in
// `internal/stdlib/core/map.fern` release argument 1 and borrow
// argument 0. Those are defined Fern functions, so the interprocedural
// fixpoint derives them and they are deliberately absent here — but a
// runtime helper of that shape would need the index.
type RcSig struct {
	Args []RcArg
}

// Arg reports the effect on argument i, if that argument carries a unit
// at all.
func (s RcSig) Arg(i int) (RcArg, bool) {
	for _, a := range s.Args {
		if a.Index == i {
			return a, true
		}
	}
	return RcArg{}, false
}

// one is the common shape: a helper that moves a count on exactly one
// argument.
func one(index int, e RcEffect, resultIsOperand bool) RcSig {
	return RcSig{Args: []RcArg{{Index: index, Effect: e, ResultIsOperand: resultIsOperand}}}
}

// rcRuntimeSigs is every runtime helper that moves a reference count,
// with its effect read off the helper's own definition in
// `internal/codegen/wasmbin/runtime.go`.
var rcRuntimeSigs = map[string]RcSig{
	"__fern_rc_inc":  one(0, RcRetain, true),
	"__fern_str_inc": one(0, RcRetain, true),

	"__fern_rc_dec":       one(0, RcRelease, true),
	"__fern_str_dec":      one(0, RcRelease, true),
	"__fern_arr_dec":      one(0, RcRelease, true),
	"__fern_box_free":     one(0, RcRelease, true),
	"__fern_cell_free":    one(0, RcRelease, true),
	"__fern_map_drop":     one(0, RcRelease, true),
	"__fern_closure_drop": one(0, RcRelease, true),
	"__fern_drop_arr_ptr": one(0, RcRelease, true),
	"__fern_drop_arr_str": one(0, RcRelease, true),
	// (base, size) with no count to read and no result: an
	// unconditional return to the freelist. The caller's unit is gone
	// either way.
	"__free": one(0, RcRelease, false),

	"__fern_rc_is_unique": one(0, RcInspect, false),

	"__fern_arr_cow_inplace":     one(0, RcMove, false),
	"__fern_arr_cow_inplace_ptr": one(0, RcMove, false),
	"__fern_arr_cow_inplace_str": one(0, RcMove, false),
	// "CONSUMES a", per its own definition; b is borrowed.
	"__fern_str_append": one(0, RcMove, false),
}

// The BUILTINS the lowering emits by name, and the rule that covers
// most of them.
//
// `providedSigs` in verifyprovided.go records every callee the IR does
// not define, and that is builtins as well as runtime helpers. The
// native backends RENAME most builtins to a runtime symbol at emit time
// — `strbuf_append` becomes `__fern_strbuf_append`, `now_ns` becomes
// `__fern_now_ns` — so a builtin is usually a helper already classified
// above under a second spelling. builtinRuntimeAlias is that rule.
//
// It only fires on an EXACT `__fern_`-prefixed match, which is what
// keeps it safe: `string_from_bytes_unchecked` lowers to
// `__fern_string_from_bytes`, the names do not correspond, the rule
// declines, and the builtin falls through to an explicit entry. Over
// the 125 unclassified names the rule answers 58 and leaves 67.
func builtinRuntimeAlias(name string) (string, bool) {
	if strings.HasPrefix(name, "__fern_") {
		return "", false
	}
	cand := "__fern_" + strings.TrimPrefix(name, "__")
	if _, ok := rcRuntimeSigs[cand]; ok {
		return cand, true
	}
	if rcInert[cand] {
		return cand, true
	}
	if _, ok := rcUnmodelled[cand]; ok {
		return cand, true
	}
	return "", false
}

// rcBuiltinSigs are the builtins that move a count and are NOT covered
// by the rename rule. Each was read on BOTH sides — the caller-side
// lowering and the callee-side decision table — because either alone
// gives the opposite answer.
var rcBuiltinSigs = map[string]RcSig{
	// (m, k, v). The key and the value are CONSUMED:
	// `calleeRetainsAnyArg` names this callee as one that "MOVES /
	// retains a fresh rc arg into a container without an inc", and
	// `emitMapSetRetains` emits the compensating retain CALLER-side
	// for an aliased argument. Reading only the second reads as "the
	// builtin borrows", which is backwards.
	//
	// The RECEIVER is borrowed, and that is a claim worth being able
	// to check rather than an omission: `m = m.set(k, v)` reassigns,
	// so the caller's own overwrite releases the old handle, and the
	// conditional retain `emitMapCowRetainTest` emits is caller-side
	// too — it exists precisely because that binding is about to be
	// overwritten.
	"__method_Map_set": {Args: []RcArg{
		{Index: 1, Effect: RcRelease},
		{Index: 2, Effect: RcRelease},
	}},
	// (arr, v). Same calleeRetainsAnyArg entry; same caller-side
	// compensation; same borrowed receiver for the same reason.
	"__method_Array_push": one(1, RcRelease, false),
	// (arr, i, v). emitArraySet retains an aliased value before the
	// store — "it is now co-owned by the buffer slot" — so the buffer
	// takes a unit and a fresh temp transfers its one reference in.
	"__method_Array_set": one(2, RcRelease, false),
	// Reads the rc word. Lowered inline rather than as a call, but
	// classified so a shadowed spelling that survives is not opaque.
	"__rc_get": one(0, RcInspect, false),
}

// rcInertBuiltins are the builtins that move no reference count on any
// argument.
//
// The bulk is the platform surface, which reads its string arguments
// and returns fresh values, plus the scalar intrinsics. The ones worth
// naming a reason for:
//
//   - `strbuf_append` memcpys the string's bytes past the buffer tail
//     (its own runtime doc), so it retains nothing.
//   - `string_from_bytes_unchecked` copies the payload into a fresh
//     string; the argument is borrowed.
//   - `cell_new` / `__method_Cell_set` hold SCALARS only in v1 (E057),
//     so there is no count on the slot to move.
//   - `__c_call*` hand raw words to a C function, which does not
//     participate in reference counting at all.
//   - `__heap_mark` / `__heap_release_to` move the arena's bump
//     pointer. "Inert" here means no ARGUMENT carries a unit — the
//     release invalidates memory wholesale, which is a different axis
//     and not one any caller of this table is asking about.
var rcInertBuiltins = map[string]bool{
	"__c_call0": true, "__c_call0_f32": true, "__c_call0_f64": true,
	"__c_call1": true, "__c_call1_f32": true, "__c_call1_f64": true,
	"__c_call2": true, "__c_call2_f32": true, "__c_call2_f64": true,
	"__c_call3": true, "__c_call3_f32": true, "__c_call3_f64": true,
	"__c_call4": true, "__c_call4_f32": true, "__c_call4_f64": true,

	"__clz32": true, "__clz64": true, "__ctz32": true, "__ctz64": true,
	"__popcount32": true, "__popcount64": true, "__round_f64": true,
	"f32_bits": true, "f32_from_bits": true,
	"f64_bits": true, "f64_from_bits": true,

	"__heap_mark": true, "__heap_release_to": true,

	"__method_Array_len": true, "__method_slice_len": true,
	"__method_string_len": true, "__method_Cell_get": true,
	"__method_Cell_set": true, "cell_new": true,

	"__method_Map_clear": true, "__method_Map_delete": true,
	"__method_Map_get": true, "__method_Map_get_or": true,
	"__method_Map_has": true, "__method_Map_iter": true,
	"__method_Map_keys": true, "__method_Map_len": true,
	"__method_Map_values": true, "map_new": true,
	"__method_MapIter_advance": true, "__method_MapIter_has_next": true,
	"__method_MapIter_key": true, "__method_MapIter_value": true,

	"__method_Reader_close": true, "__method_Reader_read_chunk": true,
	"__method_Reader_read_line": true,
	"__method_Writer_close":     true, "__method_Writer_write": true,

	"strbuf_append": true, "strbuf_reset": true, "strbuf_take": true,
	"string_from_bytes_unchecked": true,

	"proc_exec": true, "proc_fork": true, "proc_waitpid": true,
	"sleep_ms": true, "subprocess": true, "timer_fd": true,

	// (path, contents) → Result. Both strings are read and written
	// out; neither is retained. Its sibling `write_file` resolves
	// through the rename rule, but there is no
	// `__fern_write_file_exec` helper for this one to follow.
	"write_file_exec": true,
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
	"__fern_rmemchr": true, "__fern_count_byte": true,
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
	if rcInert[name] || rcInertBuiltins[name] {
		return true
	}
	if _, ok := rcBuiltinSigs[name]; ok {
		return true
	}
	_, aliased := builtinRuntimeAlias(name)
	return aliased
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
//
// `__closure_drop_` does not start `__drop_` at all, which is how it
// came to be missing: the thunk `rc_insert.go` synthesises per closure
// ends with an unconditional `__fern_closure_drop(arg0)`, so it
// releases argument 0 exactly like every other member here.
var generatedDropPrefixes = []string{
	"__closure_drop_",
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
	if s, ok := rcBuiltinSigs[name]; ok {
		return s, true
	}
	if alias, ok := builtinRuntimeAlias(name); ok {
		if s, ok := rcRuntimeSigs[alias]; ok {
			return s, true
		}
		// The alias resolved to an inert or unmodelled helper, which
		// is a classification but not a signature.
		return RcSig{}, false
	}
	if isGeneratedDrop(name) {
		return one(0, RcRelease, true), true
	}
	return RcSig{}, false
}

// isGeneratedDrop reports whether name is one of the per-type drop
// functions lowering synthesises, by the naming rule the two tables
// above describe.
//
// One copy: the result axis asks the same question, and a second
// spelling of the prefix loop is a second thing to keep in step.
func isGeneratedDrop(name string) bool {
	if generatedDropNames[name] {
		return true
	}
	for _, p := range generatedDropPrefixes {
		if strings.HasPrefix(name, p) && len(name) > len(p) {
			return true
		}
	}
	return false
}

// RcHelperUnmodelled reports whether name is a runtime helper this file
// records as moving reference counts in a shape one operand effect
// cannot express, and why.
//
// A caller that treats "no signature" as "no effect" is wrong about
// exactly these names, and right about the inert ones. Asking lets it
// tell the two apart and count the gap instead of absorbing it.
func RcHelperUnmodelled(name string) (reason string, ok bool) {
	if r, ok := rcUnmodelled[name]; ok {
		return r, true
	}
	if alias, aliased := builtinRuntimeAlias(name); aliased {
		if r, ok := rcUnmodelled[alias]; ok {
			return r, true
		}
	}
	return "", false
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
	sig, found := RcHelperSig(name)
	if !found {
		return 0, false
	}
	for _, a := range sig.Args {
		if a.Effect == RcRelease || a.Effect == RcMove {
			return a.Index, true
		}
	}
	return 0, false
}

// RcRetains reports whether a call to name adds a unit on its counted
// operand.
func RcRetains(name string) (operand int, ok bool) {
	sig, found := RcHelperSig(name)
	if !found {
		return 0, false
	}
	for _, a := range sig.Args {
		if a.Effect == RcRetain {
			return a.Index, true
		}
	}
	return 0, false
}
