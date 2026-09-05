package interp

import (
	"math"

	"github.com/jakechampion/lang/internal/codegen/fdlibm"
)

// fdlibm exp, the same algorithm the codegen backends emit, operation for
// operation. The interpreter used to delegate to Go's math.Exp, which returns
// +Inf across [709.436, 709.7827) — every argument whose k = round(x/ln2)
// reaches 1024, which is the same 2^k reconstruction defect #8237 removed from
// the backends. That left the ORACLE wrong on a band where all four backends
// are right (#8261).
//
// Products that feed an add or sub are wrapped in explicit float64
// conversions: the Go spec lets an implementation fuse a*b+c into one FMA,
// which would silently diverge from the backends' separately-rounded
// mulsd/addsd. Same fence, and same reason, as trig.go.
//
// The coefficients come from internal/codegen/fdlibm — the table the backends
// emit — rather than a copy beside it.
func fernExp(x float64) float64 {
	if math.IsNaN(x) {
		return x
	}
	// +Inf trips the overflow branch and -Inf the underflow one, so only NaN
	// needed testing separately.
	if x > fdlibm.ExpOvf {
		return math.Inf(1)
	}
	if x < fdlibm.ExpUnf {
		return 0
	}
	kf := math.RoundToEven(x * fdlibm.InvLn2)
	k := int64(kf)
	hi := x - float64(kf*fdlibm.Ln2Hi)
	lo := float64(kf * fdlibm.Ln2Lo)
	r := hi - lo
	t := r * r
	p := fdlibm.P5
	p = float64(p*t) + fdlibm.P4
	p = float64(p*t) + fdlibm.P3
	p = float64(p*t) + fdlibm.P2
	p = float64(p*t) + fdlibm.P1
	c := r - float64(p*t)
	e := fdlibm.One - ((lo - float64(r*c)/(fdlibm.Two-c)) - hi)
	// 2^k as two half-scales. One exponent field cannot hold k below -1022:
	// the low bits land in the SIGN bit there. Halving keeps both fields
	// normal, and a multiply by a power of two is exact until the result
	// itself is subnormal, so the normal band is unchanged and the subnormal
	// one rounds once.
	k1 := k >> 1
	k2 := k - k1
	s1 := math.Float64frombits(uint64(k1+1023) << 52)
	s2 := math.Float64frombits(uint64(k2+1023) << 52)
	return e * s1 * s2
}
