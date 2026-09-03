package interp

import (
	"math"
	"math/bits"

	"github.com/jakechampion/lang/internal/codegen/fdlibm"
)

// fdlibm sin/cos, the same algorithm the codegen backends emit, operation for
// operation — Cody-Waite reduction below 2^20, Payne-Hanek at and above it,
// then the ksin/kcos kernels. The interpreter used to delegate to Go's
// math.Sin/math.Cos, whose own reduction is unboundedly wrong in ulp terms
// wherever the true result approaches zero (617 ulp at 1.4e219, 3% relative
// at 6381956970095103*2^797 — see #7878), which made the interpreter useless
// as a differential lane for trig. Implementing the compiled semantics
// directly makes -interp agree with every backend bit for bit.
//
// Products that feed an add or sub are wrapped in explicit float64
// conversions: the Go spec lets an implementation fuse a*b+c into one FMA
// (and gccgo/arm64 do), which would silently diverge from the backends'
// separately-rounded mulsd/addsd. A conversion is the spec's sanctioned
// rounding fence.
//
// Agreeing bit for bit means agreeing on the numbers too, so the coefficients
// and the 2/pi limbs come from internal/codegen/fdlibm — the table the
// backends emit — rather than a copy beside it. An oracle that drifted from
// what it is checking would be the worst place for this table to diverge.

// trigRemPio2 reduces finite x to (quadrant, r) with |r| <= pi/4.
func trigRemPio2(x float64) (int64, float64) {
	// |x| >= 2^20 (biased exponent >= 1043) puts k past pio2h's 22 exact
	// mantissa bits, so the Cody-Waite chain reduces against noise there
	// and needs Payne-Hanek.
	if int(math.Float64bits(x)>>52)&0x7ff >= 1043 {
		return trigRemPio2Large(x)
	}
	kf := math.RoundToEven(x * fdlibm.TwoOPi)
	k := int64(kf)
	r := float64(float64(x-float64(kf*fdlibm.Pio2H))-float64(kf*fdlibm.Pio2M)) - float64(kf*fdlibm.Pio2L)
	return k & 3, r
}

// trigRemPio2Large is the Payne-Hanek path: multiply the significand by the
// window of 2/pi the exponent selects, keeping 128 bits about the binary
// point — the top two bits are the quadrant and the rest the fraction of
// x/(pi/2), neither of which loses accuracy with magnitude.
func trigRemPio2Large(x float64) (int64, float64) {
	b := math.Float64bits(x)
	sign := b>>63 != 0
	e := int(b>>52) & 0x7ff
	m := (b & 0x000fffffffffffff) | 0x0010000000000000
	// x = m*2^(e-1075), so the fraction of x*(2/pi) starts at bit
	// (e-1075)+62 of the table once the product is read as a Q126.
	idx := e - 1013
	off := uint(idx & 63)
	limb := idx >> 6
	t0, t1 := fdlibm.TwoOverPiBits[limb], fdlibm.TwoOverPiBits[limb+1]
	t2, t3 := fdlibm.TwoOverPiBits[limb+2], fdlibm.TwoOverPiBits[limb+3]
	// Each 64-bit window is (T[i] << off) | (T[i+1] >> (64-off)), with the
	// right half spelled as two shifts so off == 0 stays in range.
	w0 := t0<<off | t1>>1>>(63-off)
	w1 := t1<<off | t2>>1>>(63-off)
	w2 := t2<<off | t3>>1>>(63-off)
	// acc(128) = lo(m*w0)<<64 + m*w1 + hi(m*w2), i.e. x*(2/pi) as a Q126.
	hi1, p1lo := bits.Mul64(m, w1)
	hi2, _ := bits.Mul64(m, w2)
	_, p0lo := bits.Mul64(m, w0)
	lo, carry := bits.Add64(p1lo, hi2, 0)
	hi := p0lo + hi1 + carry
	q := int64(hi >> 62)
	hi &= 0x3fffffffffffffff
	neg := false
	// A fraction at or above a half belongs to the next quadrant, as the
	// negative remainder below it — which is what keeps |r| <= pi/4.
	if hi >= 0x2000000000000000 {
		q++
		borrow := uint64(0)
		if lo != 0 {
			borrow = 1
		}
		hi = 0x4000000000000000 - hi - borrow
		lo = -lo
		neg = true
	}
	// The low half only contributes below 2^-64, so 11 of its bits fall off
	// the end of the double anyway; dropping them keeps the signed
	// conversion exact, matching the backends bit for bit.
	lo >>= 11
	fr := float64(float64(int64(hi))*fdlibm.TwoM62) + float64(float64(int64(lo))*fdlibm.TwoM115)
	if neg {
		fr = -fr
	}
	if sign {
		fr = -fr
		q = -q
	}
	return q & 3, float64(fr*fdlibm.Pio2Hi) + float64(fr*fdlibm.Pio2Lo)
}

// trigKsin — sin r for |r| <= pi/4.
func trigKsin(r float64) float64 {
	z := r * r
	v := z * r
	p := fdlibm.S6
	p = float64(p*z) + fdlibm.S5
	p = float64(p*z) + fdlibm.S4
	p = float64(p*z) + fdlibm.S3
	p = float64(p*z) + fdlibm.S2
	p = float64(p*z) + fdlibm.S1
	return r + float64(p*v)
}

// trigKcos — cos r for |r| <= pi/4. The (1-w)-hz dance recovers the bits
// 1-hz discards; computing 1 - hz + z*z*p directly costs ~2 ulp.
func trigKcos(r float64) float64 {
	z := r * r
	p := fdlibm.C6
	p = float64(p*z) + fdlibm.C5
	p = float64(p*z) + fdlibm.C4
	p = float64(p*z) + fdlibm.C3
	p = float64(p*z) + fdlibm.C2
	p = float64(p*z) + fdlibm.C1
	hz := 0.5 * z
	w := 1 - hz
	return w + (((1 - w) - hz) + float64(float64(z*p)*z))
}

// trigGuard mirrors the backends' prologue: NaN returns itself, ±Inf becomes
// the canonical quiet NaN — there is no meaningful reduction of an infinite
// argument.
func trigGuard(x float64) (float64, bool) {
	if math.IsNaN(x) {
		return x, true
	}
	if math.IsInf(x, 0) {
		return math.Float64frombits(0x7ff8000000000000), true
	}
	return 0, false
}

// fernSin — quadrant 0..3 → sin r, cos r, −sin r, −cos r.
func fernSin(x float64) float64 {
	if g, done := trigGuard(x); done {
		return g
	}
	q, r := trigRemPio2(x)
	var y float64
	if q&1 == 0 {
		y = trigKsin(r)
	} else {
		y = trigKcos(r)
	}
	if q >= 2 {
		y = -y
	}
	return y
}

// fernCos — quadrant 0..3 → cos r, −sin r, −cos r, sin r.
func fernCos(x float64) float64 {
	if g, done := trigGuard(x); done {
		return g
	}
	q, r := trigRemPio2(x)
	var y float64
	if q&1 == 0 {
		y = trigKcos(r)
	} else {
		y = trigKsin(r)
	}
	if q == 1 || q == 2 {
		y = -y
	}
	return y
}
