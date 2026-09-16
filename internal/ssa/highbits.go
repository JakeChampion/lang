package ssa

// Lazy sign extension: deciding which i32 results actually need their high
// half re-established.
//
// The 64-bit backends keep an i32 sign-extended into its whole register and
// restore that after every arithmetic op — `movsxd` on x86-64, `sxtw` on
// arm64 — because ssa.Eval models an i32 as int64(int32(v)) and a 64-bit `add`
// of two in-range values can leave bits above 31 set. Across the self-host
// driver that is 49,612 `movsxd` and 28,580 `sxtw`, against four and fifteen
// from the stack-machine emitters, and on x86-64 it is 91% of the reason the
// SSA driver is larger than the one it would replace (#4112).
//
// Most of them are unnecessary, because the low 32 bits of a value are ALWAYS
// correct — the fix only restores the bits above them. So a value needs it
// only where some use reads those bits.
//
// The direction of caution is not symmetric, which is why the list below is a
// whitelist. An unnecessary fix costs three or four bytes. A missing one is a
// wrong answer: an unsigned compare against a value whose high half is garbage
// takes the wrong branch, and width.go's opening note is the same hazard in
// the other direction, where masking an ADDRESS sends its loads somewhere
// else. Anything not named here is assumed to read the whole register.

// NarrowResults says which values can skip the i32 high-half fix.
type NarrowResults struct {
	dead []bool
}

// HighBitsDead reports whether no use of v reads bits above 31, so the
// emitter can leave the register's high half holding whatever the arithmetic
// left there. False for a value the analysis never saw.
func (n *NarrowResults) HighBitsDead(v Value) bool {
	if n == nil || !v.IsValid() || int(v.ID) >= len(n.dead) {
		return false
	}
	return n.dead[v.ID]
}

// FindNarrowResults builds that answer for every value in f. Run it after
// ResolveWidths — a value it widens to 64 bits is not a candidate, because
// nothing masks one in the first place — and rebuild it after any pass that
// changes the function, exactly as with Uses.
func FindNarrowResults(f *Func, u *Uses) *NarrowResults {
	if f == nil {
		return &NarrowResults{}
	}
	n := &NarrowResults{dead: make([]bool, maxValueID(f, nil)+1)}
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			if !op.Result.IsValid() || op.Width == 64 {
				continue
			}
			narrow := true
			for _, site := range u.Of(op.Result) {
				if useReadsHighBits(site) {
					narrow = false
					break
				}
			}
			n.dead[op.Result.ID] = narrow
		}
	}
	return n
}

// useReadsHighBits reports whether one use reads bits above 31 of the value it
// reads. The whitelist is the whole of the analysis, so it is written as one.
func useReadsHighBits(site UseSite) bool {
	// A terminator use is a return value or a branch condition. A return
	// crosses into a caller that reads the register at whatever width its own
	// types say, and a condition is tested with the full register.
	if site.Op == nil {
		return true
	}
	switch site.Op.Kind {
	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpNeg:
		// The low 32 bits of the result depend only on the low 32 bits of the
		// operands, and the result carries its own fix when it needs one.
		return false
	case OpShl:
		// Same for the value shifted. Not for the COUNT: an out-of-range one
		// is left unfolded for the runtime to own (see the OpShl comment in
		// ssa.go), so what the high half holds there is not this pass's
		// business.
		return site.Index != 0
	case OpTrunc, OpExtendU, OpExtend8S, OpExtend16S:
		// Each reads 32 bits or fewer and writes the high half itself.
		return false
	case OpStore8, OpStore16, OpStore32:
		// Args[0] is the address, Args[1] the value, and the value is stored
		// at a width below 32.
		return site.Index != 1
	}
	// Everything else. Worth naming the ones that most look like they could
	// be narrow and are not: OpDivU, OpRemU, OpShrU and the unsigned compares
	// read their operands as unsigned int64 outright; the signed compares are
	// emitted at 64 bits, so a garbage high half changes the answer; OpExtendS
	// exists to spread bit 31, which has to already be right; and a call
	// argument is read by a callee that assumes its parameters arrived
	// correct.
	return true
}
