package ssa

import "github.com/jakechampion/lang/internal/ir"

// Width resolution: deciding, for every op in a module, whether its result
// occupies a full 64-bit machine register or only the low 32 bits.
//
// The distinction is load-bearing because the 64-bit backends store an i32
// sign-extended into its whole register, and re-establish that after each
// arithmetic op (`sxtw` / `movsxd`). Applied to a MACHINE ADDRESS that mask is
// destructive: any pointer above 0x7fffffff comes back negative and its loads
// and stores land somewhere else. Addresses and i32 values share the SSA's
// integer ops, so the width is what tells them apart, and the IR the lift
// consumes does not carry it — it sizes an address the same as an i32.

// ResolveWidths fixes each op's result width across a whole module, for the
// 64-bit backends (arm64 / x86-64). It seeds address-ness from what is known
// outright — the op kind, the callee's signature, the declared type of a
// parameter — then runs it to a fixpoint over the arithmetic that derives one
// address from another AND over call boundaries, and finally widens every op
// that ends up holding an address.
//
// The call-boundary half also carries plain 64-bit results: a callee returning
// i64 must not be masked back to 32 bits, and neither must one returning a
// FLOAT. Floats live in a general register as their f64 bit pattern, so an f32
// return is 32 bits of TYPE but 64 bits of REGISTER — masking one keeps the low
// mantissa half and discards the sign and exponent, which reads back as a
// denormal indistinguishable from zero.
//
// Run it after lifting and optimising all functions of a module, and before
// emit; the backends' module entry points do so themselves. wasm32 does not
// call it: a pointer there IS an i32, so nothing needs widening.
func ResolveWidths(funcs map[string]*Func) {
	r := &widthResolver{
		funcs:    funcs,
		addr:     map[*Func]map[int32]bool{},
		def:      map[*Func]map[int32]*Op{},
		paramIdx: map[*Func]map[int32]int{},
	}
	for _, f := range funcs {
		r.seed(f)
	}
	for r.step() {
	}
	for _, f := range funcs {
		for _, b := range f.Blocks {
			for _, op := range b.Ops {
				if op.Addr {
					op.Width = 64
				}
			}
		}
	}
}

type widthResolver struct {
	funcs map[string]*Func
	// addr, def and paramIdx are per-function views of one Func: which Values
	// hold a machine address, which Op defines each Value (nil for a param),
	// and which parameter position a param Value occupies.
	addr     map[*Func]map[int32]bool
	def      map[*Func]map[int32]*Op
	paramIdx map[*Func]map[int32]int
}

// seed records the address-ness that needs no analysis: the op kinds that
// always produce a heap pointer, the declared type behind a parameter, and the
// runtime-helper ABI.
func (r *widthResolver) seed(f *Func) {
	set := map[int32]bool{}
	def := map[int32]*Op{}
	idx := map[int32]int{}
	r.addr[f], r.def[f], r.paramIdx[f] = set, def, idx
	for i, p := range f.Params {
		idx[p.ID] = i
		if i < len(f.ParamAddrs) && f.ParamAddrs[i] {
			set[p.ID] = true
		}
	}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if op.Result.IsValid() {
				def[op.Result.ID] = op
			}
			if op.Result2.IsValid() {
				def[op.Result2.ID] = op
			}
			switch op.Kind {
			case OpCall, OpCallPair:
				if callee, ok := r.callee(op); ok {
					if callee.ReturnWidth == 64 || callee.ReturnFloat {
						op.Width = 64
					}
					break
				}
				if runtimeHelperWideResult[op.Str] {
					// A helper's result fills the register either as a pointer
					// or as an f64 / i64 value; only the pointer propagates
					// through address arithmetic.
					op.Width = 64
					op.Addr = op.Addr || (op.Kind == OpCall && !wideNonAddressHelpers[op.Str])
				}
			case OpAlloc, OpMakeClosure, OpMakeEnv, OpBoxDyn, OpConstVtable,
				OpConstString, OpEnumSentinel:
				op.Addr = true
			case OpLoad:
				// The pointer-width load: an i64 or a pointer, with nothing here
				// to separate them. Treating it as an address costs nothing —
				// i64 arithmetic is already 64 bits wide — and covers a pointer
				// read back out of a struct field, an array slot, or an env
				// block.
				op.Addr = true
			}
			if op.Addr && op.Result.IsValid() {
				set[op.Result.ID] = true
			}
		}
	}
}

// callee resolves a call op's target to a Func in the module, through
// ir.CodegenAlias — a Map call site names `map_new` while the module holds
// `map_new_impl`, and the backends apply the same alias where the callee
// becomes a label. Without it an aliased callee reads as an unknown runtime
// helper and its pointer result is masked back to 32 bits.
func (r *widthResolver) callee(op *Op) (*Func, bool) {
	f, ok := r.funcs[ir.CodegenAlias(op.Str)]
	return f, ok
}

// mark records that `v` holds an address in `f`, widening the op that defines
// it (or, for a parameter, the function's declared parameter shape so callers
// learn about it too). Reports whether this was news.
func (r *widthResolver) mark(f *Func, v Value) bool {
	if !v.IsValid() || r.addr[f][v.ID] {
		return false
	}
	r.addr[f][v.ID] = true
	if d := r.def[f][v.ID]; d != nil {
		d.Addr = true
		return true
	}
	if i, ok := r.paramIdx[f][v.ID]; ok {
		for len(f.ParamAddrs) < len(f.Params) {
			f.ParamAddrs = append(f.ParamAddrs, false)
		}
		f.ParamAddrs[i] = true
	}
	return true
}

// step advances the whole-module fixpoint by one round and reports whether it
// learned anything. Four propagations run together because each feeds the
// others:
//
//   - forwards, from a value that holds an address to the arithmetic that
//     offsets it and the phis that merge it;
//   - backwards, from the address operand of a load or store — whatever a
//     memory op dereferences IS an address, whether or not anything upstream
//     said so;
//   - into a callee, through the arguments a call passes, and back out through
//     the value it returns;
//   - out of a callee, to the arguments its callers pass in that position.
//
// The backwards and out-of-callee halves are what cover a pointer with no
// declared type behind it. A closure's env block arrives in a synthesised
// parameter whose IR type is a plain integer, and it reaches its captures only
// through an offset load — so the load is the only place in the program that
// says the value is an address.
func (r *widthResolver) step() bool {
	changed := false
	for _, f := range r.funcs {
		set := r.addr[f]
		for _, b := range f.Blocks {
			for _, op := range b.Ops {
				switch op.Kind {
				case OpAdd:
					if anyAddr(set, op.Args) {
						changed = r.mark(f, op.Result) || changed
					}
				case OpSub:
					// base - offset stays an address. offset - base and the
					// difference of two addresses are integers, and both keep
					// Args[0] as the value the result is measured from.
					if len(op.Args) > 0 && set[op.Args[0].ID] {
						changed = r.mark(f, op.Result) || changed
					}
				case OpPhi:
					if anyAddr(set, op.Args) {
						changed = r.mark(f, op.Result) || changed
					}
				case OpSelect:
					if len(op.Args) == 3 && (set[op.Args[1].ID] || set[op.Args[2].ID]) {
						changed = r.mark(f, op.Result) || changed
					}
				case OpLoad, OpLoad8S, OpLoad8U, OpLoad16S, OpLoad16U, OpLoad32U,
					OpLoadF, OpStore, OpStore8, OpStore16, OpStore32, OpStoreF:
					if len(op.Args) > 0 {
						changed = r.mark(f, op.Args[0]) || changed
					}
				}
			}
			if (b.Term.Kind == TermRet || b.Term.Kind == TermRetPair) &&
				b.Term.Value.IsValid() && set[b.Term.Value.ID] && !f.ReturnAddr {
				f.ReturnAddr = true
				changed = true
			}
		}
	}
	for _, f := range r.funcs {
		set := r.addr[f]
		for _, b := range f.Blocks {
			for _, op := range b.Ops {
				if op.Kind != OpCall && op.Kind != OpCallPair {
					continue
				}
				callee, ok := r.callee(op)
				if !ok {
					continue
				}
				if op.Kind == OpCall && callee.ReturnAddr {
					changed = r.mark(f, op.Result) || changed
				}
				if len(op.Args) != len(callee.Params) {
					continue
				}
				for i, a := range op.Args {
					if set[a.ID] {
						changed = r.mark(callee, callee.Params[i]) || changed
					}
					if i < len(callee.ParamAddrs) && callee.ParamAddrs[i] {
						changed = r.mark(f, a) || changed
					}
				}
			}
		}
	}
	return changed
}

func anyAddr(addr map[int32]bool, args []Value) bool {
	for _, a := range args {
		if addr[a.ID] {
			return true
		}
	}
	return false
}

// runtimeHelperWideResult names the backend-provided runtime helpers whose
// result fills a whole 64-bit register — a heap pointer, or an f64 bit pattern.
// The lift cannot read these off a signature (a helper has no ssa.Func), so the
// ABI the backends implement is written down here instead. A name absent from
// the map returns void or an i32, for which the i32 mask is correct.
//
// Every helper a backend emits must be classified: arm64ssa's
// TestEveryRuntimeHelperResultIsClassified fails on one that is not.
var runtimeHelperWideResult = map[string]bool{
	"__str_concat":                  true,
	"__str_slice":                   true,
	"__str_idx":                     true,
	"__load_ptr":                    true,
	"__load_i64":                    true,
	"__memcpy":                      true,
	"__fern_map_drop":               true,
	"__alloc":                       true,
	"__alloc_reuse":                 true,
	"__alloc_u8":                    true,
	"__fern_box_free":               true,
	"__fern_rc_inc":                 true,
	"__fern_rc_dec":                 true,
	"__fern_str_dec":                true,
	"__fern_arr_dec":                true,
	"__fern_drop_arr_ptr":           true,
	"__fern_drop_arr_str":           true,
	"__fern_closure_drop":           true,
	"__fern_io_error":               true,
	"__fern_arr_push_grow":          true,
	"__fern_arr_push_grow_ptr":      true,
	"__fern_arr_push_grow_str":      true,
	"__fern_arr_push_grow_move_ptr": true,
	"__fern_arr_push_grow_move_str": true,
	"__fern_arr_cow_inplace":        true,
	"string_from_bytes_unchecked":   true,
	"args":                          true,
	"env":                           true,
	"strbuf_take":                   true,
	"write_file":                    true,
	"read_file":                     true,
	"remove_file":                   true,
	"create_dir_all":                true,
	"remove_dir_all":                true,
	"temp_dir":                      true,
	"read_dir":                      true,
	"random_bytes":                  true,
	"tcp_recv":                      true,
	"open_writer":                   true,
	"open_reader":                   true,
	"open_appender":                 true,
	"__method_Writer_write":         true,
	"__method_Writer_close":         true,
	"__method_Reader_read_chunk":    true,
	"__method_Reader_read_line":     true,
	"__method_Reader_close":         true,
	"stdin":                         true,
	"stdout":                        true,
	"stderr":                        true,
	"__arr_idx":                     true,
	"__arr_idx_1":                   true,
	"__arr_idx_8":                   true,
	"__arr_idx_16":                  true,
	"__arr_idx_nc":                  true,
	"__arr_idx_1_nc":                true,
	"__arr_idx_8_nc":                true,
	"__arr_idx_16_nc":               true,
	"__abs_f64":                     true,
	"__sqrt_f64":                    true,
	"__floor_f64":                   true,
	"__ceil_f64":                    true,
	"__trunc_f64":                   true,
	"__round_f64":                   true,
	"__exp_f64":                     true,
	"__log_f64":                     true,
	"__pow_f64":                     true,
	"__sin_f64":                     true,
	"__cos_f64":                     true,
}

// wideNonAddressHelpers is the subset of runtimeHelperWideResult whose 64 bits
// are an f64 bit pattern or an i64 value rather than a machine address, so
// nothing derived from one is an address either.
var wideNonAddressHelpers = map[string]bool{
	"__abs_f64":   true,
	"__sqrt_f64":  true,
	"__floor_f64": true,
	"__ceil_f64":  true,
	"__trunc_f64": true,
	"__round_f64": true,
	"__exp_f64":   true,
	"__log_f64":   true,
	"__pow_f64":   true,
	"__sin_f64":   true,
	"__cos_f64":   true,
	"__load_i64":  true,
}

// RuntimeHelperResultClassified reports whether `name` appears in either helper
// table. A backend's completeness test uses it to prove that every helper it
// emits has an answer here, since an unlisted one is treated as narrow and
// would have its pointer result truncated without ever failing a test.
func RuntimeHelperResultClassified(name string) bool {
	return runtimeHelperWideResult[name] || narrowRuntimeHelpers[name]
}

// narrowRuntimeHelpers names the backend-provided helpers whose result is void
// or a genuine i32 — the ones for which the i32 sign-extend mask is correct and
// must stay. It exists so that a newly added helper belongs to neither map and
// trips the completeness test, rather than silently defaulting to narrow.
var narrowRuntimeHelpers = map[string]bool{
	"__str_len":            true,
	"__str_eq":             true,
	"__str_ord":            true,
	"__ptr_width":          true,
	"__load_i32":           true,
	"__store_i32":          true,
	"__store_ptr":          true,
	"__store_i64":          true,
	"__free":               true,
	"__fern_rc_is_unique":  true,
	"__memset":             true,
	"__fern_map_hash_seed": true,
	"__fern_memchr":        true,
	"__fern_ascii_run":     true,
	"isatty":               true,
	"poll":                 true,
	"print":                true,
	"write":                true,
	"eprint":               true,
	"putchar":              true,
	"exit":                 true,
	"strbuf_reset":         true,
	"strbuf_append":        true,
	"random_i32":           true,
	"tcp_listen":           true,
	"tcp_accept":           true,
	"tcp_send":             true,
	"tcp_close":            true,
	"tcp_pollable":         true,
	"wasm_timer_pollable":  true,
	"wasm_poll":            true,
	"wasm_pollable_drop":   true,
	"wasm_block":           true,
}
