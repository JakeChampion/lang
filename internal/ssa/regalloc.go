package ssa

import "sort"

// Register allocation, phase 1 of the SSA-level allocator (#4112): live
// intervals + a linear-scan allocator. This is still pure analysis — it
// produces a Value → physical-register | spill-slot assignment but emits no
// code. A later phase consumes the assignment in an SSA→native emitter.
//
// The allocator uses single (hole-free) live intervals per value — the classic
// Poletto–Sarazin linear scan. A single interval over-approximates liveness for
// values that are live across a gap (e.g. around a loop), which is always
// *safe* for allocation (it can only make the allocator more conservative,
// never assign two simultaneously-live values the same register), at the cost
// of occasionally spilling where a hole-aware allocator would not. Refining to
// interval lists is a later optimisation.

// Target describes the register file the allocator targets. Phase 1 keeps this
// minimal — just the count of interchangeable allocatable registers. Fixed-
// register constraints (call-arg/return registers, x86 idiv's rax/rdx, the
// shift-count register) and the caller/callee-saved split are layered on in the
// emit phase, where the ABI is concrete.
type Target struct {
	// NumRegs is the number of allocatable physical registers. Values that do
	// not fit are spilled to stack slots.
	NumRegs int
	// CalleeSaved[r] reports whether allocatable register r maps to a
	// callee-saved physical register on this target — one preserved across a
	// call for free (the callee restores it), so a value living across a call is
	// cheaper there than in a caller-saved register (which the caller must spill
	// around every call). The allocator prefers callee-saved registers for
	// call-crossing values and caller-saved for the rest. Nil (or shorter than
	// NumRegs) means "no preference": every register is treated as caller-saved,
	// so allocation is unchanged.
	CalleeSaved []bool
}

// Interval is a value's single live range over linearised program points,
// inclusive of both ends. Start is the value's definition (or live-in) point;
// End is its last use (or live-out) point.
type Interval struct {
	Value int32
	Start int
	End   int
}

// Allocation is the result of linear scan: each live value is assigned either a
// physical register (Reg) or a spill slot (Slot), never both.
type Allocation struct {
	// Reg maps a Value ID to a physical register index in [0, Target.NumRegs).
	Reg map[int32]int
	// Slot maps a spilled Value ID to a stack-slot index in [0, NumSlots).
	Slot map[int32]int
	// NumSlots is the number of distinct spill slots used.
	NumSlots int
	// Intervals is the live interval per value, retained for verification and
	// for the emit phase (spill/reload placement).
	Intervals map[int32]Interval
	// OpPos maps each op to its program point in the same space the Intervals
	// live in, so the emit phase can locate a call among the intervals (a value
	// whose interval strictly spans a call's point is live across it). Nil until
	// LinearScan populates it.
	OpPos map[*Op]int
	// CallLive maps each call op to the Value IDs live immediately after it —
	// the values the callee must not clobber, hence the ones the caller saves.
	// Nil until LinearScan populates it.
	CallLive map[*Op]map[int32]bool
}

// LiveAcrossOp returns the values live across the call op `op`, and whether the
// answer is known. It is LiveAcross with the imprecision taken out: live
// intervals are hole-free, so a value defined early and used late spans every
// call in between whether or not it is live there, and each of those calls then
// saves and reloads a register nothing will read. The per-point walk in
// callLiveSets answers exactly instead.
//
// Under-saving would be a miscompile, so it matters that this can only shrink
// the set safely: an interval covers every point at which its value is really
// live, and no two values whose intervals overlap share a register, so at the
// call's point the register holding a value the walk drops holds nothing else
// that is live either.
func (a *Allocation) LiveAcrossOp(op *Op) (map[int32]bool, bool) {
	s, ok := a.CallLive[op]
	return s, ok
}

// callLiveSets walks each block backwards from its live-out set, so that at
// every op it holds exactly the values live immediately after that op, and
// records that set at each call.
func callLiveSets(f *Func, live *Liveness) map[*Op]map[int32]bool {
	out := map[*Op]map[int32]bool{}
	for _, b := range f.Blocks {
		cur := map[int32]bool{}
		for id := range live.LiveOut[b] {
			cur[id] = true
		}
		// A terminator reads its operands at the block's exit, after every op.
		for _, v := range termUses(b.Term) {
			if v.IsValid() {
				cur[v.ID] = true
			}
		}
		for i := len(b.Ops) - 1; i >= 0; i-- {
			op := b.Ops[i]
			if isCallOp(op.Kind) {
				s := make(map[int32]bool, len(cur))
				for id := range cur {
					s[id] = true
				}
				// The call defines its results AT the call, so they are not
				// live across it however long they live afterwards.
				if op.Result.IsValid() {
					delete(s, op.Result.ID)
				}
				if op.Result2.IsValid() {
					delete(s, op.Result2.ID)
				}
				out[op] = s
			}
			if op.Result.IsValid() {
				delete(cur, op.Result.ID)
			}
			if op.Result2.IsValid() {
				delete(cur, op.Result2.ID)
			}
			// A phi reads its args on the incoming edges, not here, so they are
			// live-out of the predecessors rather than live before the phi.
			if op.Kind == OpPhi {
				continue
			}
			for _, arg := range op.Args {
				if arg.IsValid() {
					cur[arg.ID] = true
				}
			}
		}
	}
	return out
}

// LiveAcross returns the set of Value IDs whose live interval strictly spans the
// program point p — i.e. defined before p and still live after it. At a call's
// point this is the set of values the callee must not clobber; the caller saves
// (only) the registers holding them. A value defined at p (e.g. the call result)
// or last-used at p (e.g. an argument) does not span it.
func (a *Allocation) LiveAcross(p int) map[int32]bool {
	out := map[int32]bool{}
	for id, iv := range a.Intervals {
		if iv.Start < p && iv.End > p {
			out[id] = true
		}
	}
	return out
}

// linearizePoints assigns a program point to each block entry, each op, and each
// block exit, monotonically increasing over the RPO order — the point space the
// live intervals live in. Shared by LiveIntervals (interval construction) and the
// emit phase (locating a call site among the intervals for call-clobber-aware
// saves), so both agree on the numbering.
func linearizePoints(f *Func) (opPos map[*Op]int, blockStart, blockEnd map[*Block]int) {
	blockStart = map[*Block]int{}
	blockEnd = map[*Block]int{}
	opPos = map[*Op]int{}
	pos := 0
	for _, b := range f.RPO() {
		blockStart[b] = pos
		pos++
		for _, op := range b.Ops {
			opPos[op] = pos
			pos++
		}
		blockEnd[b] = pos // the terminator / block-exit point
		pos++
	}
	return opPos, blockStart, blockEnd
}

// LiveIntervals linearises the blocks in RPO and derives one conservative live
// interval per value from the block-level liveness sets.
func LiveIntervals(f *Func, live *Liveness) map[int32]Interval {
	// Points increase monotonically in RPO so an interval's [start,end] is a
	// contiguous range over the linear order.
	opPos, blockStart, blockEnd := linearizePoints(f)

	iv := map[int32]Interval{}
	extend := func(id int32, p int) {
		cur, ok := iv[id]
		if !ok {
			iv[id] = Interval{Value: id, Start: p, End: p}
			return
		}
		if p < cur.Start {
			cur.Start = p
		}
		if p > cur.End {
			cur.End = p
		}
		iv[id] = cur
	}

	for _, b := range f.RPO() {
		s, e := blockStart[b], blockEnd[b]
		// Anything live on entry is live from the block's start; anything live
		// on exit is live to the block's end. Together with the per-op defs/uses
		// below, this stitches a value's range across the blocks it spans.
		for id := range live.LiveIn[b] {
			extend(id, s)
		}
		for id := range live.LiveOut[b] {
			extend(id, e)
		}
		for _, op := range b.Ops {
			p := opPos[op]
			if op.Kind == OpPhi {
				// Phi args are edge-uses (already captured by the predecessors'
				// live-out above); only the phi result is defined here.
				if op.Result.IsValid() {
					extend(op.Result.ID, p)
				}
				continue
			}
			for _, a := range op.Args {
				if a.IsValid() {
					extend(a.ID, p)
				}
			}
			if op.Result.IsValid() {
				extend(op.Result.ID, p)
			}
			if op.Result2.IsValid() {
				extend(op.Result2.ID, p)
			}
		}
		// Terminator operands are used at the block's exit point.
		for _, v := range termUses(b.Term) {
			if v.IsValid() {
				extend(v.ID, e)
			}
		}
	}
	return iv
}

// LinearScan computes liveness + live intervals for f and runs linear-scan
// register allocation against the given target, returning the assignment.
func LinearScan(f *Func, target Target) *Allocation {
	live := ComputeLiveness(f)
	iv := LiveIntervals(f, live)
	opPos, _, _ := linearizePoints(f)
	callLive := callLiveSets(f, live)
	// A value is call-crossing if it is live across any call — the allocator
	// steers those toward callee-saved registers.
	crosses := map[int32]bool{}
	for _, s := range callLive {
		for id := range s {
			crosses[id] = true
		}
	}
	alloc := allocateLinear(iv, target, crosses)
	alloc.OpPos = opPos
	alloc.CallLive = callLive
	return alloc
}

// isCallOp reports whether an op transfers control to a callee (so values live
// across it must survive a call). The dynamic-dispatch pair belong here too:
// OpCallDyn calls through a vtable slot, and OpBoxDyn calls the allocator.
func isCallOp(k OpKind) bool {
	switch k {
	case OpCall, OpCallPair, OpCallIndirect, OpCallDyn, OpBoxDyn:
		return true
	}
	return false
}

// allocateLinear is the register-assignment core, separated from interval
// construction so it can be unit-tested on hand-built interval sets.
func allocateLinear(iv map[int32]Interval, target Target, crosses map[int32]bool) *Allocation {
	order := make([]Interval, 0, len(iv))
	for _, i := range iv {
		order = append(order, i)
	}
	// Increasing start, tie-broken by value ID for determinism.
	sort.Slice(order, func(a, b int) bool {
		if order[a].Start != order[b].Start {
			return order[a].Start < order[b].Start
		}
		return order[a].Value < order[b].Value
	})

	alloc := &Allocation{
		Reg:       map[int32]int{},
		Slot:      map[int32]int{},
		Intervals: iv,
	}

	// Free register indices, lowest first for determinism.
	free := make([]int, 0, target.NumRegs)
	for r := target.NumRegs - 1; r >= 0; r-- {
		free = append(free, r) // pop from the back => lowest index first
	}
	// active holds intervals currently in registers, sorted by increasing End.
	var active []Interval

	calleeSaved := func(r int) bool {
		return r < len(target.CalleeSaved) && target.CalleeSaved[r]
	}
	// pickReg removes and returns a free register, preferring one whose
	// callee-saved class matches wantCalleeSaved (so call-crossing values land in
	// callee-saved registers and everything else in caller-saved ones); it falls
	// back to any free register. Ties break to the lowest index for determinism.
	// With no CalleeSaved hint every register is caller-saved, so this reduces to
	// "pick the lowest free register" — the previous behaviour.
	pickReg := func(wantCalleeSaved bool) int {
		best := -1
		for _, r := range free {
			if calleeSaved(r) != wantCalleeSaved {
				continue
			}
			if best == -1 || r < best {
				best = r
			}
		}
		if best == -1 { // no register of the preferred class; take any
			for _, r := range free {
				if best == -1 || r < best {
					best = r
				}
			}
		}
		for idx, r := range free {
			if r == best {
				free = append(free[:idx], free[idx+1:]...)
				break
			}
		}
		return best
	}
	addActive := func(i Interval) {
		active = append(active, i)
		sort.Slice(active, func(a, b int) bool {
			if active[a].End != active[b].End {
				return active[a].End < active[b].End
			}
			return active[a].Value < active[b].Value
		})
	}

	nextSlot := 0
	spill := func(id int32) {
		alloc.Slot[id] = nextSlot
		nextSlot++
	}

	for _, i := range order {
		// Expire intervals that ended strictly before i starts. Equal endpoints
		// are treated as overlapping (conservative), so a value whose last use
		// coincides with another's definition does not share a register.
		kept := active[:0]
		for _, a := range active {
			if a.End < i.Start {
				free = append(free, alloc.Reg[a.Value]) // register returns to the pool
			} else {
				kept = append(kept, a)
			}
		}
		active = kept

		if len(free) == 0 {
			// No register available: spill the interval that ends last, between
			// i and the current furthest-ending active interval.
			last := active[len(active)-1]
			if last.End > i.End {
				// Steal last's register for i; spill last.
				alloc.Reg[i.Value] = alloc.Reg[last.Value]
				delete(alloc.Reg, last.Value)
				spill(last.Value)
				// Replace last with i in active.
				active = active[:len(active)-1]
				addActive(i)
			} else {
				spill(i.Value)
			}
			continue
		}
		alloc.Reg[i.Value] = pickReg(crosses[i.Value])
		addActive(i)
	}

	alloc.NumSlots = nextSlot
	return alloc
}

// VerifyAllocation checks the core correctness invariant: no two values whose
// live intervals overlap are assigned the same physical register, and every
// value is assigned exactly one location (register xor slot). Returns a
// human-readable description of the first violation, or "" if the allocation is
// sound. Intended for tests and as a debug assertion behind the emit phase.
func VerifyAllocation(a *Allocation) string {
	ids := make([]int32, 0, len(a.Intervals))
	for id := range a.Intervals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		_, hasReg := a.Reg[id]
		_, hasSlot := a.Slot[id]
		if hasReg == hasSlot { // neither, or both
			return locDesc(id, hasReg, hasSlot)
		}
	}
	for ii := 0; ii < len(ids); ii++ {
		for jj := ii + 1; jj < len(ids); jj++ {
			i, j := a.Intervals[ids[ii]], a.Intervals[ids[jj]]
			if !intervalsOverlap(i, j) {
				continue
			}
			ri, iok := a.Reg[i.Value]
			rj, jok := a.Reg[j.Value]
			if iok && jok && ri == rj {
				return overlapDesc(i, j, ri)
			}
		}
	}
	return ""
}

// intervalsOverlap reports whether two intervals share any point. Inclusive on
// both ends — matching the conservative expiry rule in the allocator.
func intervalsOverlap(a, b Interval) bool {
	return a.Start <= b.End && b.Start <= a.End
}

func locDesc(id int32, hasReg, hasSlot bool) string {
	switch {
	case hasReg && hasSlot:
		return "v" + itoa(id) + " assigned both a register and a spill slot"
	default:
		return "v" + itoa(id) + " assigned no location"
	}
}

func overlapDesc(a, b Interval, reg int) string {
	return "overlapping values v" + itoa(a.Value) + " [" + itoa(int32(a.Start)) + "," + itoa(int32(a.End)) +
		"] and v" + itoa(b.Value) + " [" + itoa(int32(b.Start)) + "," + itoa(int32(b.End)) +
		"] share register r" + itoa(int32(reg))
}

// itoa is a tiny int32→string helper to keep this file free of fmt for hot
// paths; the descriptions are only built on a verification failure.
func itoa(n int32) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
