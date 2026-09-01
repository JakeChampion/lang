package interp

import (
	"math"
	"math/bits"
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

// twoOverPiBits is 2/pi in binary, MSB-first, one limb per 64 fraction bits
// starting at 2^-1 in limb 1 — the window Payne-Hanek indexes with the
// argument's own exponent. The leading zero limb lets that index start above
// 2^-1 without a bounds test; the length covers the largest finite double.
// Same table as the backends': carried per implementation site, like the
// fdlibm coefficients beside it.
var twoOverPiBits = [21]uint64{
	0x0000000000000000, 0xa2f9836e4e441529, 0xfc2757d1f534ddc0,
	0xdb6295993c439041, 0xfe5163abdebbc561, 0xb7246e3a424dd2e0,
	0x06492eea09d1921c, 0xfe1deb1cb129a73e, 0xe88235f52ebb4484,
	0xe99c7026b45f7e41, 0x3991d639835339f4, 0x9c845f8bbdf9283b,
	0x1ff897ffde05980f, 0xef2f118b5a0a6d1f, 0x6d367ecf27cb09b7,
	0x4f463f669e5fea2d, 0x7527bac7ebe5f17b, 0x3d0739f78a5292ea,
	0x6bfb5fb11f8d5d08, 0x56033046fc7b6bab, 0xf0cfbc209af4361d,
}

const (
	trigTwoOPi = 6.36619772367581382433e-01
	// pi/2 as three 33-bit chunks (~99 bits) for the Cody-Waite path.
	trigPio2h = 1.57079632673412561417e+00
	trigPio2m = 6.07710050630396597660e-11
	trigPio2l = 2.02226624879595063154e-21
	// pi/2 as an unevaluated double-double, plus the two scales that turn
	// the Payne-Hanek 126-bit fraction into a double.
	trigPio2Hi = 1.5707963267948966
	trigPio2Lo = 6.123233995736766e-17
	trigTwoM62 = 2.168404344971009e-19
	trigTwoM11 = 2.407412430484045e-35
)

// trigRemPio2 reduces finite x to (quadrant, r) with |r| <= pi/4.
func trigRemPio2(x float64) (int64, float64) {
	// |x| >= 2^20 (biased exponent >= 1043) puts k past pio2h's 22 exact
	// mantissa bits, so the Cody-Waite chain reduces against noise there
	// and needs Payne-Hanek.
	if int(math.Float64bits(x)>>52)&0x7ff >= 1043 {
		return trigRemPio2Large(x)
	}
	kf := math.RoundToEven(x * trigTwoOPi)
	k := int64(kf)
	r := float64(float64(x-float64(kf*trigPio2h))-float64(kf*trigPio2m)) - float64(kf*trigPio2l)
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
	t0, t1 := twoOverPiBits[limb], twoOverPiBits[limb+1]
	t2, t3 := twoOverPiBits[limb+2], twoOverPiBits[limb+3]
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
	fr := float64(float64(int64(hi))*trigTwoM62) + float64(float64(int64(lo))*trigTwoM11)
	if neg {
		fr = -fr
	}
	if sign {
		fr = -fr
		q = -q
	}
	return q & 3, float64(fr*trigPio2Hi) + float64(fr*trigPio2Lo)
}

// trigKsin — sin r for |r| <= pi/4.
func trigKsin(r float64) float64 {
	z := r * r
	v := z * r
	p := 1.58969099521155010221e-10
	p = float64(p*z) - 2.50507602534068634195e-08
	p = float64(p*z) + 2.75573137070700676789e-06
	p = float64(p*z) - 1.98412698298579493134e-04
	p = float64(p*z) + 8.33333333332248946124e-03
	p = float64(p*z) - 1.66666666666666324348e-01
	return r + float64(p*v)
}

// trigKcos — cos r for |r| <= pi/4. The (1-w)-hz dance recovers the bits
// 1-hz discards; computing 1 - hz + z*z*p directly costs ~2 ulp.
func trigKcos(r float64) float64 {
	z := r * r
	p := -1.13596475577881948265e-11
	p = float64(p*z) + 2.08757232129817482790e-09
	p = float64(p*z) - 2.75573143513906633035e-07
	p = float64(p*z) + 2.48015872894767294178e-05
	p = float64(p*z) - 1.38888888888741095749e-03
	p = float64(p*z) + 4.16666666666666019037e-02
	hz := 0.5 * z
	w := 1 - hz
	return w + (((1-w)-hz)+float64(float64(z*p)*z))
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
