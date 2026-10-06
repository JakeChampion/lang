package interp

import (
	"math"

	"github.com/jakechampion/lang/internal/tables/fdlibm"
)

// Table-driven log, the algorithm the codegen backends emit, operation for
// operation; internal/tables/fdlibm/logtab.go documents it and holds its data. The
// interpreter used to delegate to Go's math.Log, which recovers k from the
// STORED exponent field and so answers ln(2^-1022) ~ -709.09 for every
// subnormal argument, whatever its magnitude — the same defect #8497 removed
// from the backends. That left the ORACLE wrong on a band where all four
// backends are right.
//
// Products that feed an add or sub are wrapped in explicit float64
// conversions: the Go spec lets an implementation fuse a*b+c into one FMA,
// which would silently diverge from the backends' separately-rounded
// mulsd/addsd. Same fence, and same reason, as trig.go and exp.go.
func fernLog(x float64) float64 {
	ix := math.Float64bits(x)
	if ix-fdlibm.LogNear1Lo < fdlibm.LogNear1Hi-fdlibm.LogNear1Lo {
		return fernLogNear1(x)
	}
	if top := ix >> 52; top-1 >= 0x7fe {
		switch {
		case ix<<1 == 0:
			return math.Inf(-1)
		case math.IsNaN(x) || math.IsInf(x, 1):
			return x
		case top >= 0x800:
			return math.NaN()
		}
		// A subnormal stores exponent 0, its magnitude in the mantissa's
		// leading zeros; scaling by 2^52 moves it into the field, and taking
		// 52 off the field leaves the reduction below a k it cannot store.
		ix = math.Float64bits(x*0x1p52) - 52<<52
	}
	tmp := ix - fdlibm.LogOff
	i := (tmp >> (52 - fdlibm.LogTableBits)) % (1 << fdlibm.LogTableBits)
	k := int64(tmp) >> 52
	iz := ix - tmp&(0xfff<<52)
	z := math.Float64frombits(iz)
	// c, the midpoint of z's subinterval, is z with the bits below the index
	// replaced by their halfway point; z - c is exact.
	c := math.Float64frombits(iz&^(1<<(52-fdlibm.LogTableBits)-1) | 1<<(51-fdlibm.LogTableBits))
	t := &fdlibm.LogTable[i]
	a := &fdlibm.LogPoly

	r := float64(float64(z-c) * t[0])
	kd := float64(k)
	w := float64(kd*fdlibm.Ln2Hi) + t[1]
	hi := w + r
	lo := float64(w-hi) + r + (float64(kd*fdlibm.Ln2Lo) + t[2])
	r2 := r * r
	p := float64(a[0]+float64(r*a[1])) + float64(r2*float64(a[2]+float64(r*a[3])))
	return float64(lo-float64(r2*0.5)) + float64(float64(r*r2)*p) + hi
}

// fernLogNear1 is log x for x in [1 - 2^-4, 1 + 0x1.09p-4], where the result
// is small enough that the table path's absolute error would show: log1p of
// the exact r = x - 1, with r - r²/2 carried as hi + lo.
func fernLogNear1(x float64) float64 {
	b := &fdlibm.LogNear1Poly
	r := x - 1
	r2 := r * r
	r3 := r * r2
	p := float64(b[6]+float64(r*b[7])) + float64(r2*b[8]) + float64(r3*b[9])
	p = float64(b[3]+float64(r*b[4])) + float64(r2*b[5]) + float64(r3*p)
	p = float64(b[0]+float64(r*b[1])) + float64(r2*b[2]) + float64(r3*p)
	y := float64(r3 * p)
	// rhi keeps r's top 26 bits, so rhi*rhi is exact.
	w := float64(r * 0x1p27)
	rhi := float64(r+w) - w
	rlo := r - rhi
	h := float64(float64(rhi*rhi) * 0.5)
	hi := r - h
	lo := float64(r-hi) - h
	lo -= float64(float64(rlo*0.5) * float64(rhi+r))
	return float64(y+lo) + hi
}
