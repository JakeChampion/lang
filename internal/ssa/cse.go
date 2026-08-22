package ssa

import (
	"math"
	"strconv"
	"strings"
)

// CSE — common subexpression elimination. Walks the function
// in reverse-postorder and, for every pure Op, looks up its
// canonical form in an "expression key → first Value" map.
// On a hit, the duplicate Op's Result is aliased to the
// canonical one; uses elsewhere in the function are rewritten
// to point at the canonical Value. Pair with DCE to reclaim
// the now-orphan duplicates.
//
// "Pure" excludes:
//   - OpCall, OpLoad, OpStore — side-effecting; can't dedup.
//   - OpPhi — its meaning depends on incoming pred order +
//     the block it lives in, so cross-block dedup would be
//     unsound.
//
// Coverage rule (RPO walk + dominance): we only treat two
// Ops as candidates for merging if the FIRST occurrence's
// block dominates the second's. Otherwise the rewrite could
// move a use of the canonical Value to a program point where
// it isn't defined. With pure ops that's still safe within
// a block; across blocks we rely on the dominator tree to
// confirm.
//
// Single pass converges on most real programs because RPO
// guarantees we see defs before any use; a second CSE call
// catches the rare cases where rewriting unlocked a new
// merge (chains like `(a+b) + (a+b) + (a+b)`). Callers that
// care about completeness can run in a fixed-point loop with
// DCE between iterations.
func CSE(f *Func) {
	if f == nil || f.Entry == nil {
		return
	}
	dom := BuildDomTree(f)
	rpo := dom.RPO()

	type entry struct {
		val   Value
		block *Block
	}
	table := map[string]entry{}
	sub := map[int32]Value{}

	for _, b := range rpo {
		for _, op := range b.Ops {
			if !cseEligible(op) {
				continue
			}
			args := resolveArgs(op.Args, sub)
			key := exprKey(op, args)

			if prev, ok := table[key]; ok && dom.Dominates(prev.block, b) {
				sub[op.Result.ID] = prev.val
				continue
			}
			// First occurrence (or no dominating prior occurrence):
			// canonicalise *this* op's Args via the sub map so
			// later lookups see the same Arg representation.
			op.Args = args
			table[key] = entry{val: op.Result, block: b}
		}
	}

	if len(sub) == 0 {
		return
	}
	applySubstitutions(f, sub)
}

func cseEligible(op *Op) bool {
	if op == nil || !op.Result.IsValid() {
		return false
	}
	// Phi + Invalid are pure-ish (no memory effect) but can't be
	// CSE'd: a phi's meaning depends on the (block + Preds)
	// context, and Invalid is a sentinel that should never
	// appear in well-formed SSA.
	if op.Kind == OpPhi || op.Kind == OpInvalid {
		return false
	}
	// Everything else: eligibility tracks purity.
	return IsPure(op.Kind)
}

func resolveArgs(args []Value, sub map[int32]Value) []Value {
	if len(args) == 0 || len(sub) == 0 {
		return args
	}
	out := make([]Value, len(args))
	for i, a := range args {
		out[i] = resolveValue(a, sub)
	}
	return out
}

func resolveValue(v Value, sub map[int32]Value) Value {
	seen := map[int32]bool{}
	for v.IsValid() {
		if seen[v.ID] {
			return v
		}
		seen[v.ID] = true
		next, ok := sub[v.ID]
		if !ok {
			return v
		}
		v = next
	}
	return v
}

// exprKey returns a stable canonical key for `op` after Args
// have been resolved through the substitution map. The key
// folds Kind, Imm, F64, Str, and the Arg ID sequence into one
// string — cheap to build, hash-table-friendly.
func exprKey(op *Op, args []Value) string {
	var sb strings.Builder
	sb.WriteString(op.Kind.String())
	sb.WriteByte('|')
	sb.WriteString(strconv.FormatInt(op.Imm, 10))
	sb.WriteByte('|')
	// Keyed on the bit pattern, not a decimal rendering: every NaN formats
	// alike, so two NaNs with different payloads are distinct constants.
	sb.WriteString(strconv.FormatUint(math.Float64bits(op.F64), 16))
	sb.WriteByte('|')
	sb.WriteString(strconv.Quote(op.Str))
	sb.WriteByte('|')
	for i, a := range args {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatInt(int64(a.ID), 10))
	}
	return sb.String()
}

// applySubstitutions rewrites every Args slot + terminator
// operand to use the canonical Value when the original was
// merged into another expression. Identical shape to
// Simplify's second pass but lives separately so the two
// can evolve independently.
func applySubstitutions(f *Func, sub map[int32]Value) {
	for _, b := range f.Blocks {
		for _, op := range b.Ops {
			for i := range op.Args {
				op.Args[i] = resolveValue(op.Args[i], sub)
			}
		}
		b.Term.Cond = resolveValue(b.Term.Cond, sub)
		b.Term.Value = resolveValue(b.Term.Value, sub)
		b.Term.Value2 = resolveValue(b.Term.Value2, sub)
	}
}
