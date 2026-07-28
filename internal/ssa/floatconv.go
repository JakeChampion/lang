package ssa

import "math"

// Float-to-int conversion in Fern SATURATES, identically on every backend
// (docs/FLOAT-SEMANTICS.md): NaN yields 0, a value above the destination's max
// yields that max, one below its min yields that min, and anything in range
// truncates toward zero. It never traps and never leaks a platform sentinel.
//
// Go's own float→int conversion does none of that — it is undefined for NaN
// and out-of-range values, and in practice wraps — so every place in this
// package that evaluates one of these ops at compile time has to clamp
// explicitly. `(91.23f32 * 1e9) as i32` folded to 1035689984 on the SSA path
// where the interpreter, arm64 and x86-64 all produce 2147483647.
//
// Width follows the lift's convention for these ops: 64 for an i64/u64
// destination, 0 (or 32) for i32/u32.

// satFToIS converts a float to a signed integer with saturation.
func satFToIS(v float64, width int8) int64 {
	if math.IsNaN(v) {
		return 0
	}
	if width == 64 {
		if v >= math.MaxInt64 {
			return math.MaxInt64
		}
		if v <= math.MinInt64 {
			return math.MinInt64
		}
		return int64(v)
	}
	if v >= math.MaxInt32 {
		return math.MaxInt32
	}
	if v <= math.MinInt32 {
		return math.MinInt32
	}
	return int64(int32(v))
}

// satFToIU converts a float to an unsigned integer with saturation. The result
// is returned in the int64 storage the SSA model uses for integer values: a u32
// is sign-extended from bit 31, matching maskFix and the unsigned-compare
// convention in evalBinaryInt.
func satFToIU(v float64, width int8) int64 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if width == 64 {
		if v >= math.MaxUint64 {
			return -1 // all-ones: UINT64_MAX in the int64 storage
		}
		return int64(uint64(v))
	}
	if v >= math.MaxUint32 {
		return -1 // all-ones: UINT32_MAX sign-extended from bit 31
	}
	return int64(int32(uint32(v)))
}
