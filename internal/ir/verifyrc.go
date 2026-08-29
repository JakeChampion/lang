// The IR verifier's ownership half.
//
// verify.go checks the invariants a backend needs to emit anything at
// all, and verifystack.go checks that the operand stack is discipline-
// obeying. Neither knows what a reference count is — so the one place
// where Perceus writes into a box the program still holds a name for,
// the reuse protocol, is checked by nothing.
//
// A reuse site lowers to a fixed shape: test a donor local for sole
// ownership, take its box as the allocation token on the unique arm,
// release it on the shared arm, and hand the token to the reuse
// allocator. Three of those steps name a local, and the protocol is only
// safe when all three name the SAME one:
//
//	reused = is_unique(D)        // the GATE
//	if reused { token = base(D) } // the TOKEN — D's box is given away
//	else      { dec D }           // the DECLINE release
//	box = alloc_reuse(token, ...)
//
// When the gate tests D1 and the token takes D2, the site writes the new
// value over a box whose uniqueness nothing established — D2 may have
// other owners, which is a destructive write to live memory, and D1 is
// released by nobody. That is not hypothetical: the pairing tables are
// keyed lookups, and docs/rc-log/2026-08-29-xblock-recipient-site-key.md
// records exactly this — a first-match read that let one if-arm's
// recipient resolve the OTHER arm's donor. It was closed by reasoning
// rather than by observation, because reaching the destructive path
// needs a coincidence: a 200-round probe measured 0 on both sides. A
// check over the emitted ops does not need the coincidence.
//
// The invariant holds across every emitter and both compilers. Native
// spells the gate OpRcIsUnique and calls `__alloc_reuse` with three
// arguments; the self-host spells it as a call to `__fern_rc_is_unique`
// and calls `__fern_alloc_reuse` with two; emitReuseToken zeroes the
// donor slot after the select where the two overwrite emitters do not.
// None of that is what makes a site safe. One donor is.
//
// # Fail-soft, and why
//
// Same stance as verifystack.go, for the same reason: a verifier that
// reports a false problem gets switched off. A site whose shape this
// pass does not recognise is SKIPPED and counted, never reported. The
// count is the honest signal — an emitter that grows a new reuse shape
// shows up as a coverage regression rather than as a spurious failure,
// and it is per SITE rather than per function, so one unfamiliar shape
// does not blind the pass to the familiar sites around it.
package ir

import (
	"fmt"
	"sort"
)

// RcCoverage records how many reuse sites the ownership pass could
// model. Checked + the Skipped totals == Sites.
type RcCoverage struct {
	Sites   int
	Checked int
	// Skipped counts the unrecognised sites by cause.
	Skipped map[string]int
}

func (c *RcCoverage) skip(reason string) {
	if c.Skipped == nil {
		c.Skipped = map[string]int{}
	}
	c.Skipped[reason]++
}

// Reasons returns the skip causes, most frequent first.
func (c RcCoverage) Reasons() []string {
	out := make([]string, 0, len(c.Skipped))
	for r := range c.Skipped {
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool {
		if c.Skipped[out[a]] != c.Skipped[out[b]] {
			return c.Skipped[out[a]] > c.Skipped[out[b]]
		}
		return out[a] < out[b]
	})
	return out
}

// add folds other into c, so a whole-program coverage can be summed from
// per-function ones.
func (c *RcCoverage) add(other RcCoverage) {
	c.Sites += other.Sites
	c.Checked += other.Checked
	for r, n := range other.Skipped {
		if c.Skipped == nil {
			c.Skipped = map[string]int{}
		}
		c.Skipped[r] += n
	}
}

// isReuseAlloc reports whether name is the reuse allocator. The two
// spellings are the native lowering's and the self-host's; they differ
// in arity, which is why the token argument is found by walking back
// over the size constants rather than by a fixed offset.
func isReuseAlloc(name string) bool {
	return name == "__alloc_reuse" || name == "__fern_alloc_reuse"
}

func isIsUniqueOp(op Op) bool {
	return op.Kind == OpRcIsUnique ||
		(op.Kind == OpCallDirect && op.Str == "__fern_rc_is_unique")
}

func isRcDecOp(op Op) bool {
	return op.Kind == OpRcDec ||
		(op.Kind == OpCallDirect && op.Str == "__fern_rc_dec")
}

type rcChecker struct {
	f        *Func
	problems []Problem
	cov      RcCoverage
}

func (c *rcChecker) report(i int, k OpKind, format string, args ...any) {
	c.problems = append(c.problems, Problem{
		Func: c.f.Name, Op: i, Kind: k, Msg: fmt.Sprintf(format, args...),
	})
}

// verifyRc checks every reuse site in f. Unlike the stack half it never
// abandons a whole function: a site it cannot model costs only that
// site's coverage.
func verifyRc(f *Func) ([]Problem, RcCoverage) {
	c := &rcChecker{f: f}
	for i, op := range f.Ops {
		if op.Kind != OpCallDirect || !isReuseAlloc(op.Str) {
			continue
		}
		c.cov.Sites++
		c.site(i)
	}
	return c.problems, c.cov
}

// site checks the one reuse site whose allocator call is at op index
// call.
func (c *rcChecker) site(call int) {
	ops := c.f.Ops

	// The token is the allocator's first argument. Every later argument
	// is a size constant, so walking back over the constant run reaches
	// the load that pushed the token whatever the arity is.
	j := call - 1
	for j >= 0 && ops[j].Kind == OpConstI32 {
		j--
	}
	if j < 0 || ops[j].Kind != OpLoadLocal {
		c.cov.skip("token argument is not a local load")
		return
	}
	tokenSlot := ops[j].I32

	// emitReuseToken zeroes the donor's slot between the select and the
	// call ("D consumed — zero its slot"); the two overwrite emitters
	// leave the slot alone. Step over the zeroing when it is there.
	k := j - 1
	if k >= 1 && ops[k].Kind == OpStoreLocal &&
		ops[k-1].Kind == OpConstI32 && ops[k-1].I32 == 0 {
		k -= 2
	}
	if k < 0 || ops[k].Kind != OpEnd {
		c.cov.skip("no token select before the reuse call")
		return
	}
	ifAt, elseAt, ok := matchIfBackwards(ops, k)
	if !ok {
		c.cov.skip("token select is not a well-formed if")
		return
	}
	if elseAt < 0 {
		c.cov.skip("token select has no decline arm")
		return
	}

	gate, gateCode := gateDonor(ops, ifAt)
	if gateCode == gateMissing {
		c.cov.skip("no uniqueness gate before the token select")
		return
	}
	if gateCode == gateNotAFlag {
		c.cov.skip("token select is not conditioned on a local")
		return
	}

	// The reuse arm: exactly one store to the token slot, fed by a load
	// of the donor. Native subtracts the rc header to reach the box base
	// (`LoadLocal D; ConstI32 hdr; Sub`); the self-host stores the
	// pointer as-is.
	token, ok := soleTokenSource(ops, ifAt+1, elseAt, tokenSlot)
	if !ok {
		c.cov.skip("reuse arm does not derive the token from one local")
		return
	}

	// The decline arm usually releases the donor, but not always: the
	// self-host's struct-reuse family releases it earlier under a separate
	// condition and leaves the decline arm holding only the null token. An
	// absent release is therefore not a defect — there is simply no third
	// name to disagree. TWO releases are a different matter: nothing can say
	// which one the protocol meant, so the site is skipped.
	dec, decAt, haveDec, ok := soleDecline(ops, elseAt+1, k)
	if !ok {
		c.cov.skip("decline arm releases more than one local")
		return
	}

	c.cov.Checked++

	if gate != token {
		c.report(call, OpCallDirect,
			"reuse site takes local %d's box as its allocation token, but tested local %d for sole ownership "+
				"(gate at op %d, token at op %d): the box being written over was never proved unique",
			token, gate, ifAt-3, ifAt+1)
	}
	if haveDec && dec != gate {
		c.report(call, OpCallDirect,
			"reuse site's decline arm releases local %d, but the uniqueness gate tested local %d "+
				"(gate at op %d, release at op %d): the tested donor leaks on the decline path",
			dec, gate, ifAt-3, decAt)
	}
}

// gateDonor reads the uniqueness gate sitting before the token select and
// returns the donor it tested, or the reason the gate was not recognised.
//
// Four spellings occur across the two compilers' emitters, differing only in
// where the is_unique flag goes on its way to the branch and in how the donor
// reaches is_unique. None of them changes what the gate means.
//
//	load_local D;              is_unique;                  If  — flag straight to the branch
//	load_local D;              is_unique; tee_local u;      If  — flag also kept in a slot
//	load_local D;              is_unique; store_local u; load_local u; If
//	load_local D; tee_local X; is_unique; tee_local u;      If  — donor also kept in a slot
//
// In the last of those the donor is X, the slot the tee wrote, because that is
// the slot the reuse arm loads for its token.
//
// The failure is returned as a code rather than a message so every skip reason
// in this file stays a literal at its cov.skip call, which is what
// TestSelfHostIRVerifyRcSkipReasonsMatchNative reads to compare this pass's
// coverage vocabulary against the self-host's.
func gateDonor(ops []Op, ifAt int) (slot int32, code int) {
	// Step back over however the flag reached the branch.
	at := ifAt - 1
	switch {
	case at >= 0 && isIsUniqueOp(ops[at]):
		// flag consumed directly by the if
	case at >= 0 && ops[at].Kind == OpTeeLocal:
		at--
	case at >= 1 && ops[at].Kind == OpLoadLocal &&
		ops[at-1].Kind == OpStoreLocal && ops[at-1].I32 == ops[at].I32:
		at -= 2
	default:
		return 0, gateNotAFlag
	}
	if at < 1 || !isIsUniqueOp(ops[at]) {
		return 0, gateMissing
	}
	// The donor is whatever fed is_unique: a plain load, or a tee whose slot
	// the reuse arm will load back.
	src := ops[at-1]
	if src.Kind != OpLoadLocal && src.Kind != OpTeeLocal {
		return 0, gateMissing
	}
	return src.I32, gateOK
}

// gateDonor's verdicts.
const (
	gateOK = iota
	gateMissing
	gateNotAFlag
)

// matchIfBackwards finds the OpIf that the OpEnd at endAt closes, and
// that scope's OpElse. elseAt is -1 when the if has no else arm.
func matchIfBackwards(ops []Op, endAt int) (ifAt, elseAt int, ok bool) {
	elseAt = -1
	depth := 0
	for i := endAt - 1; i >= 0; i-- {
		switch ops[i].Kind {
		case OpEnd:
			depth++
		case OpBlock, OpLoop:
			if depth == 0 {
				return 0, -1, false // an unclosed scope opens inside ours
			}
			depth--
		case OpIf:
			if depth == 0 {
				return i, elseAt, true
			}
			depth--
		case OpElse:
			if depth == 0 {
				elseAt = i
			}
		}
	}
	return 0, -1, false
}

// soleTokenSource returns the local whose value the reuse arm stores
// into tokenSlot. It reports false unless the arm does that exactly
// once, at its own nesting depth, from a single local load.
func soleTokenSource(ops []Op, from, to int, tokenSlot int32) (int32, bool) {
	src, found := int32(0), false
	for i, depth := from, 0; i < to; i++ {
		switch ops[i].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
			continue
		case OpEnd:
			depth--
			continue
		}
		if depth != 0 || ops[i].Kind != OpStoreLocal || ops[i].I32 != tokenSlot {
			continue
		}
		if found {
			return 0, false
		}
		// `LoadLocal D` on its own, or `LoadLocal D; ConstI32 hdr; Sub`
		// where the constant steps back over the rc header to the base.
		switch {
		case i-1 >= from && ops[i-1].Kind == OpLoadLocal:
			src = ops[i-1].I32
		case i-3 >= from && ops[i-1].Kind == OpSub &&
			ops[i-2].Kind == OpConstI32 && ops[i-3].Kind == OpLoadLocal:
			src = ops[i-3].I32
		default:
			return 0, false
		}
		found = true
	}
	return src, found
}

// soleDecline returns the local the decline arm releases and where, whether
// it found one at all, and whether the arm was modellable.
//
// The arm may hold none (the release happened earlier under a separate
// condition) or exactly one. More than one, or one whose operand is not a
// plain local load, is unmodellable — emitReuseToken also RETAINS the
// consuming-match bindings there, so "the arm's only reference-count op"
// would be the wrong rule; "the arm's only release" is the right one.
func soleDecline(ops []Op, from, to int) (slot int32, at int, found, ok bool) {
	for i, depth := from, 0; i < to; i++ {
		switch ops[i].Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
			continue
		case OpEnd:
			depth--
			continue
		}
		if depth != 0 || !isRcDecOp(ops[i]) {
			continue
		}
		if found || i-1 < from || ops[i-1].Kind != OpLoadLocal {
			return 0, 0, false, false
		}
		slot, at, found = ops[i-1].I32, i, true
	}
	return slot, at, found, true
}
