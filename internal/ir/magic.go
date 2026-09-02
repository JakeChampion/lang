// Magic-number reciprocals: the constants that turn a division by a
// compile-time constant into a multiply-high plus a shift.
//
// The derivation is Granlund–Montgomery as presented in Hacker's Delight
// figures 10-1 (signed) and 10-3 (unsigned). Both pick the smallest p for
// which the truncated reciprocal 2^p/d, rounded up, reproduces `x / d`
// exactly for every dividend at the operand width — the loop is a search for
// that p, not an approximation with a tolerance.
//
// Only the 32-bit families are derived here. The IR expresses the
// multiply-high by widening to i64, multiplying, and shifting 32 down; there
// is no op for the high half of a 64x64 multiply, and the four-limb expansion
// wasm would need to emulate one costs more than the divide it replaces.

package ir

// MagicS32 is the signed 32-bit reciprocal for d. The quotient is
//
//	q  = mulhi_s(x, M)          // (i64)x * M >> 32
//	q += x    if Add            // M's sign disagrees with d's
//	q -= x    if Sub
//	q >>= S                     // arithmetic
//	q += (unsigned)q >> 31      // +1 for a negative quotient: round toward zero
//
// Add and Sub are never both set. d must not be 0, ±1, ±2^k or INT_MIN —
// those have cheaper lowerings (or, for INT_MIN, no positive magnitude).
type MagicS32 struct {
	M   int32
	S   uint
	Add bool
	Sub bool
}

// DeriveMagicS32 returns the signed reciprocal for d.
func DeriveMagicS32(d int32) MagicS32 {
	ad := uint32(d)
	if d < 0 {
		ad = uint32(-d)
	}
	const two31 = uint32(1) << 31
	// t = 2^31 + (sign bit of d): the bound on |nc| is one larger for a
	// negative divisor, because the negative side of the range is one wider.
	t := two31 + uint32(d)>>31
	anc := t - 1 - t%ad // |nc|, the largest |x| with x % d == d-1

	p := uint(31)
	q1, r1 := two31/anc, two31-(two31/anc)*anc
	q2, r2 := two31/ad, two31-(two31/ad)*ad
	for {
		p++
		q1, r1 = 2*q1, 2*r1
		if r1 >= anc {
			q1, r1 = q1+1, r1-anc
		}
		q2, r2 = 2*q2, 2*r2
		if r2 >= ad {
			q2, r2 = q2+1, r2-ad
		}
		delta := ad - r2
		if q1 > delta || (q1 == delta && r1 != 0) {
			break
		}
	}

	m := int32(q2 + 1)
	if d < 0 {
		m = -m
	}
	mg := MagicS32{M: m, S: p - 32}
	// A magic whose sign disagrees with the divisor's has wrapped past
	// 2^31; adding (or subtracting) the dividend back recovers the
	// product's true high half.
	mg.Add = d > 0 && m < 0
	mg.Sub = d < 0 && m > 0
	return mg
}

// MagicU32 is the unsigned 32-bit reciprocal for d. The quotient is
//
//	h = mulhi_u(x, M)                        // (u64)x * M >> 32
//	q = h >> S                               if !Add
//	q = (((x - h) >>u 1) + h) >> (S - 1)     if Add
//
// Add marks a magic that needs 33 bits. Rather than widen M, the shift-average
// form above computes (x + h) / 2 without the carry out of bit 31 that a plain
// add would lose. d must not be 0, 1 or a power of two.
type MagicU32 struct {
	M   uint32
	S   uint
	Add bool
}

// DeriveMagicU32 returns the unsigned reciprocal for d.
func DeriveMagicU32(d uint32) MagicU32 {
	var mg MagicU32
	const two31 = uint32(1) << 31
	var zero uint32
	nc := zero - 1 - (zero-d)%d

	p := uint(31)
	q1, r1 := two31/nc, two31-(two31/nc)*nc
	q2, r2 := (two31-1)/d, (two31-1)-((two31-1)/d)*d
	for {
		p++
		if r1 >= nc-r1 {
			q1, r1 = 2*q1+1, 2*r1-nc
		} else {
			q1, r1 = 2*q1, 2*r1
		}
		if r2+1 >= d-r2 {
			if q2 >= two31-1 {
				mg.Add = true
			}
			q2, r2 = 2*q2+1, 2*r2+1-d
		} else {
			if q2 >= two31 {
				mg.Add = true
			}
			q2, r2 = 2*q2, 2*r2+1
		}
		delta := d - 1 - r2
		if p >= 64 || (q1 > delta || (q1 == delta && r1 != 0)) {
			break
		}
	}
	mg.M = q2 + 1
	mg.S = p - 32
	return mg
}
