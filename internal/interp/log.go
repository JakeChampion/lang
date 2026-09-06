package interp

import (
	"math"

	"github.com/jakechampion/lang/internal/codegen/fdlibm"
)

// fdlibm log, the same algorithm the codegen backends emit, operation for
// operation. The interpreter used to delegate to Go's math.Log, which recovers
// k from the STORED exponent field and so answers ln(2^-1022) ~ -709.09 for
// every subnormal argument, whatever its magnitude — the same defect #8497
// removed from the backends. That left the ORACLE wrong on a band where all
// four backends are right.
//
// Products that feed an add or sub are wrapped in explicit float64
// conversions: the Go spec lets an implementation fuse a*b+c into one FMA,
// which would silently diverge from the backends' separately-rounded
// mulsd/addsd. Same fence, and same reason, as trig.go and exp.go.
//
// The coefficients come from internal/codegen/fdlibm — the table the backends
// emit — rather than a copy beside it.
func fernLog(x float64) float64 {
	if math.IsNaN(x) {
		return x
	}
	if x < 0 {
		return math.NaN()
	}
	if x == 0 {
		return math.Inf(-1)
	}
	if math.IsInf(x, 1) {
		return x
	}
	// A subnormal stores exponent 0, so the field below would report the
	// smallest normal exponent for every one of them. Scaling into the
	// normal range moves the magnitude back into the field; k carries the
	// scale off again.
	kadj := int64(0)
	if x < fdlibm.MinNorm {
		x *= fdlibm.Two54
		kadj = 54
	}
	bits := math.Float64bits(x)
	k := int64((bits>>52)&0x7ff) - 1023 - kadj
	m := math.Float64frombits((bits & 0xfffffffffffff) | 0x3ff0000000000000)
	if m >= fdlibm.Sqrt2 {
		m *= fdlibm.Half
		k++
	}
	f := m - fdlibm.One
	s := f / (fdlibm.Two + f)
	z := s * s
	w := z * z
	t1 := fdlibm.Lg6
	t1 = float64(t1*w) + fdlibm.Lg4
	t1 = float64(t1*w) + fdlibm.Lg2
	t1 *= w
	t2 := fdlibm.Lg7
	t2 = float64(t2*w) + fdlibm.Lg5
	t2 = float64(t2*w) + fdlibm.Lg3
	t2 = float64(t2*w) + fdlibm.Lg1
	t2 *= z
	r := t1 + t2
	hfsq := fdlibm.Half * float64(f*f)
	kf := float64(k)
	return float64(kf*fdlibm.Ln2Hi) -
		((hfsq - (float64(s*(hfsq+r)) + float64(kf*fdlibm.Ln2Lo))) - f)
}
