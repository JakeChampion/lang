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
// integer ops, so the width is what tells them apart, and the IR sizes an
// address the same as an i32 everywhere except a call, where the result
// classification rides on the op (ir.ResAddr / ResWide / ResNarrow).

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
// always produce a heap pointer, the declared type behind a parameter, and
// what a call's callee returns.
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
				// A callee the module defines answers for itself. One it
				// does not — a backend-provided builtin or runtime helper —
				// has no signature to read, so the lift stamped the answer
				// onto the op from the IR (ir.ResAddr / ResWide / ResNarrow)
				// before this pass ran.
				if callee, ok := r.callee(op); ok {
					if callee.ReturnWidth == 64 || callee.ReturnFloat {
						op.Width = 64
					}
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
	d := r.def[f][v.ID]
	if d != nil && isIntConst(d.Kind) {
		return false
	}
	r.addr[f][v.ID] = true
	if d != nil {
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

// isIntConst reports whether `k` defines an integer literal. Such a value is
// never a machine address: every address in a Fern program comes from an
// allocation, a runtime helper, or a string constant, all of which seed
// address-ness directly.
//
// The distinction matters because a literal is the one value in the SSA that is
// genuinely polymorphic. CSE merges by (kind, operands) and the IR carries no
// type, so the null pointer a drop call passes and the integer zero a loop
// counter starts at become ONE value. Marking it would make every use of that
// zero an address — including the i32 uses — and the fixpoint then carries that
// through the callees they reach. Refusing costs nothing: a literal small enough
// to be an i32 sign-extends to itself, and a wider one already has Width 64 from
// the lift, which this pass only ever raises.
func isIntConst(k OpKind) bool {
	return k == OpConstInt || k == OpConstBool
}

func anyAddr(addr map[int32]bool, args []Value) bool {
	for _, a := range args {
		if addr[a.ID] {
			return true
		}
	}
	return false
}
