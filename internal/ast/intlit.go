package ast

import (
	"fmt"
	"strconv"
)

// IntLitOutOfRange returns the E047 message for an integer literal that t
// cannot hold, or "" when it fits. The literal is judged as the source wrote
// it: a NumberLit carries only the magnitude and the sign is a separate unary
// node, so `-2147483648` is 2^31 negated (in range for i32) while
// `2147483648` is not, and both share a NumberLit. The checker and constfold
// both report through this so a `var` and a `const` refuse the same literals
// with the same words.
func IntLitOutOfRange(lit *NumberLit, negated bool, t NumberType) string {
	msg := fmt.Sprintf("literal %s does not fit in %s", intLitText(lit, negated), t)
	if lit.ExceedsU64 {
		return msg
	}
	// Past i64 max Value holds the wrapped bit pattern, and uint64 reads the
	// written magnitude back in every case.
	magnitude := uint64(lit.Value)
	if magnitude == 0 {
		negated = false
	}
	if t.IsSigned() {
		var limit uint64
		switch t.NormalWidth() {
		case 32:
			limit = 1<<31 - 1
		case 64:
			limit = 1<<63 - 1
		default:
			return ""
		}
		if negated {
			limit++
		}
		if magnitude > limit {
			return msg
		}
		return ""
	}
	if negated {
		return msg + ": unsigned types have no negative values"
	}
	var max uint64
	switch t.NormalWidth() {
	case 8:
		max = 1<<8 - 1
	case 32:
		max = 1<<32 - 1
	default:
		// u64 holds every bit pattern; usize's width is the target's.
		return ""
	}
	if magnitude > max {
		return msg
	}
	return ""
}

// intLitText renders the literal the way the source wrote it, sign included,
// so a diagnostic never quotes a number the author did not type.
func intLitText(lit *NumberLit, negated bool) string {
	sign := ""
	if negated {
		sign = "-"
	}
	if lit.Raw != "" {
		return sign + lit.Raw
	}
	if lit.ExceedsI64 {
		return sign + strconv.FormatUint(uint64(lit.Value), 10)
	}
	return sign + strconv.FormatInt(lit.Value, 10)
}
