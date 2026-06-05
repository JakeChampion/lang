package ssa

import "math"

// Width-aware constant folding for integer ops.
//
// SSA integer ops carry an `Op.Width` of 0 (i32, the default) or 64
// (i64). Folding must compute the result at that width: an i32 op wraps
// at 32 bits, masks its shift count to 0..31, and compares unsigned
// operands as u32. The old folders computed every integer op in int64
// and stored the full 64-bit result, so `2_000_000_000 + 2_000_000_000`
// folded to 4_000_000_000 instead of the i32-wrapped -294_967_296 and a
// following `< 0` test folded to the wrong answer. These helpers mirror
// foldBinary (i32) / foldBinary64 (i64) in internal/ir/fold.go — the
// blessed reference folder. See docs/ADVERSARIAL-REVIEW-2026-06.md (I1).

// negAtWidth negates v at the given integer width (wrapping at 32 bits
// when not w64).
func negAtWidth(w64 bool, v int64) int64 {
	if w64 {
		return -v
	}
	return int64(-int32(v))
}

// foldIntBinaryAtWidth folds an integer binary op on constant operands
// at the op's width. Returns (intResult, isBool, boolResult, ok).
// ok=false means the op must be left intact — either it is not a
// foldable integer binary op, or folding it would discard a runtime trap
// (divide-by-zero, or the INT_MIN / -1 signed-division overflow, both of
// which trap on wasm/native).
func foldIntBinaryAtWidth(k OpKind, w64 bool, lhs, rhs int64) (res int64, isBool, boolRes, ok bool) {
	if w64 {
		return foldIntBinary64(k, lhs, rhs)
	}
	return foldIntBinary32(k, lhs, rhs)
}

func foldIntBinary32(k OpKind, lhs, rhs int64) (int64, bool, bool, bool) {
	a, b := int32(lhs), int32(rhs)
	switch k {
	case OpAdd:
		return int64(a + b), false, false, true
	case OpSub:
		return int64(a - b), false, false, true
	case OpMul:
		return int64(a * b), false, false, true
	case OpDiv:
		if b == 0 || (a == math.MinInt32 && b == -1) {
			return 0, false, false, false
		}
		return int64(a / b), false, false, true
	case OpDivU:
		if b == 0 {
			return 0, false, false, false
		}
		return int64(int32(uint32(a) / uint32(b))), false, false, true
	case OpRem:
		if b == 0 {
			return 0, false, false, false
		}
		if a == math.MinInt32 && b == -1 {
			return 0, false, false, true // no trap; result is 0
		}
		return int64(a % b), false, false, true
	case OpRemU:
		if b == 0 {
			return 0, false, false, false
		}
		return int64(int32(uint32(a) % uint32(b))), false, false, true
	case OpAnd:
		return int64(a & b), false, false, true
	case OpOr:
		return int64(a | b), false, false, true
	case OpXor:
		return int64(a ^ b), false, false, true
	case OpShl:
		return int64(a << (uint32(b) & 31)), false, false, true
	case OpShr:
		return int64(a >> (uint32(b) & 31)), false, false, true
	case OpShrU:
		return int64(int32(uint32(a) >> (uint32(b) & 31))), false, false, true
	case OpEq:
		return 0, true, a == b, true
	case OpNe:
		return 0, true, a != b, true
	case OpLt:
		return 0, true, a < b, true
	case OpLtU:
		return 0, true, uint32(a) < uint32(b), true
	case OpLe:
		return 0, true, a <= b, true
	case OpLeU:
		return 0, true, uint32(a) <= uint32(b), true
	case OpGt:
		return 0, true, a > b, true
	case OpGtU:
		return 0, true, uint32(a) > uint32(b), true
	case OpGe:
		return 0, true, a >= b, true
	case OpGeU:
		return 0, true, uint32(a) >= uint32(b), true
	}
	return 0, false, false, false
}

func foldIntBinary64(k OpKind, a, b int64) (int64, bool, bool, bool) {
	switch k {
	case OpAdd:
		return a + b, false, false, true
	case OpSub:
		return a - b, false, false, true
	case OpMul:
		return a * b, false, false, true
	case OpDiv:
		if b == 0 || (a == math.MinInt64 && b == -1) {
			return 0, false, false, false
		}
		return a / b, false, false, true
	case OpDivU:
		if b == 0 {
			return 0, false, false, false
		}
		return int64(uint64(a) / uint64(b)), false, false, true
	case OpRem:
		if b == 0 {
			return 0, false, false, false
		}
		if a == math.MinInt64 && b == -1 {
			return 0, false, false, true // no trap; result is 0
		}
		return a % b, false, false, true
	case OpRemU:
		if b == 0 {
			return 0, false, false, false
		}
		return int64(uint64(a) % uint64(b)), false, false, true
	case OpAnd:
		return a & b, false, false, true
	case OpOr:
		return a | b, false, false, true
	case OpXor:
		return a ^ b, false, false, true
	case OpShl:
		return a << (uint64(b) & 63), false, false, true
	case OpShr:
		return a >> (uint64(b) & 63), false, false, true
	case OpShrU:
		return int64(uint64(a) >> (uint64(b) & 63)), false, false, true
	case OpEq:
		return 0, true, a == b, true
	case OpNe:
		return 0, true, a != b, true
	case OpLt:
		return 0, true, a < b, true
	case OpLtU:
		return 0, true, uint64(a) < uint64(b), true
	case OpLe:
		return 0, true, a <= b, true
	case OpLeU:
		return 0, true, uint64(a) <= uint64(b), true
	case OpGt:
		return 0, true, a > b, true
	case OpGtU:
		return 0, true, uint64(a) > uint64(b), true
	case OpGe:
		return 0, true, a >= b, true
	case OpGeU:
		return 0, true, uint64(a) >= uint64(b), true
	}
	return 0, false, false, false
}
