// The reference derivation ir.fern's derive_magic_s / derive_magic_u are
// pinned against (self_host_ir_strength_test.go's magicParityLines).
//
// Granlund–Montgomery as presented in Hacker's Delight figures 10-1 (signed)
// and 10-3 (unsigned): the smallest p for which the truncated reciprocal 2^p/d,
// rounded up, reproduces `x / d` exactly for every dividend at the width.

package e2ecompiler

import "math/bits"

// magicS32 is the signed 32-bit reciprocal for d. The quotient is
//
//	q  = mulhi_s(x, M)          // (i64)x * M >> 32
//	q += x    if Add            // M's sign disagrees with d's
//	q -= x    if Sub
//	q >>= S                     // arithmetic
//	q += (unsigned)q >> 31      // +1 for a negative quotient: round toward zero
//
// Add and Sub are never both set. d must not be 0, ±1, ±2^k or INT_MIN —
// those have cheaper lowerings (or, for INT_MIN, no positive magnitude).
type magicS32 struct {
	M   int32
	S   uint
	Add bool
	Sub bool
}

// magicS64 is magicS32 at 64 bits: the high half is of the 128-bit product,
// and the rounding fixup adds bit 63.
type magicS64 struct {
	M   int64
	S   uint
	Add bool
	Sub bool
}

// deriveMagicS32 returns the signed reciprocal for d.
func deriveMagicS32(d int32) magicS32 {
	m, s, add, sub := deriveMagicSW[int32, uint32](d)
	return magicS32{M: m, S: s, Add: add, Sub: sub}
}

// deriveMagicS64 returns the signed reciprocal for d.
func deriveMagicS64(d int64) magicS64 {
	m, s, add, sub := deriveMagicSW[int64, uint64](d)
	return magicS64{M: m, S: s, Add: add, Sub: sub}
}

// deriveMagicSW derives the signed reciprocal at the width of S, with U its
// unsigned twin.
func deriveMagicSW[S ~int32 | ~int64, U ~uint32 | ~uint64](d S) (m S, sh uint, add, sub bool) {
	w := uint(bits.OnesCount64(uint64(^U(0))))
	ad := U(d)
	if d < 0 {
		ad = U(-d)
	}
	two := U(1) << (w - 1)
	// t = 2^(w-1) + (sign bit of d): the bound on |nc| is one larger for a
	// negative divisor, because the negative side of the range is one wider.
	t := two + U(d)>>(w-1)
	anc := t - 1 - t%ad // |nc|, the largest |x| with x % d == d-1

	p := w - 1
	q1, r1 := two/anc, two-(two/anc)*anc
	q2, r2 := two/ad, two-(two/ad)*ad
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

	m = S(q2 + 1)
	if d < 0 {
		m = -m
	}
	// A magic whose sign disagrees with the divisor's has wrapped past
	// 2^(w-1); adding (or subtracting) the dividend back recovers the
	// product's true high half.
	return m, p - w, d > 0 && m < 0, d < 0 && m > 0
}

// magicU32 is the unsigned 32-bit reciprocal for d. The quotient is
//
//	h = mulhi_u(x, M)                        // (u64)x * M >> 32
//	q = h >> S                               if !Add
//	q = (((x - h) >>u 1) + h) >> (S - 1)     if Add
//
// Add marks a magic that needs 33 bits. Rather than widen M, the shift-average
// form above computes (x + h) / 2 without the carry out of bit 31 that a plain
// add would lose. d must not be 0, 1 or a power of two.
type magicU32 struct {
	M   uint32
	S   uint
	Add bool
}

// magicU64 is magicU32 at 64 bits: the high half is of the 128-bit product,
// and Add marks a 65-bit magic.
type magicU64 struct {
	M   uint64
	S   uint
	Add bool
}

// deriveMagicU32 returns the unsigned reciprocal for d.
func deriveMagicU32(d uint32) magicU32 {
	m, s, add := deriveMagicUW(d)
	return magicU32{M: m, S: s, Add: add}
}

// deriveMagicU64 returns the unsigned reciprocal for d.
func deriveMagicU64(d uint64) magicU64 {
	m, s, add := deriveMagicUW(d)
	return magicU64{M: m, S: s, Add: add}
}

func deriveMagicUW[U ~uint32 | ~uint64](d U) (m U, sh uint, add bool) {
	w := uint(bits.OnesCount64(uint64(^U(0))))
	two := U(1) << (w - 1)
	var zero U
	nc := zero - 1 - (zero-d)%d

	p := w - 1
	q1, r1 := two/nc, two-(two/nc)*nc
	q2, r2 := (two-1)/d, (two-1)-((two-1)/d)*d
	for {
		p++
		if r1 >= nc-r1 {
			q1, r1 = 2*q1+1, 2*r1-nc
		} else {
			q1, r1 = 2*q1, 2*r1
		}
		if r2+1 >= d-r2 {
			if q2 >= two-1 {
				add = true
			}
			q2, r2 = 2*q2+1, 2*r2+1-d
		} else {
			if q2 >= two {
				add = true
			}
			q2, r2 = 2*q2, 2*r2+1
		}
		delta := d - 1 - r2
		if p >= 2*w || (q1 > delta || (q1 == delta && r1 != 0)) {
			break
		}
	}
	return q2 + 1, p - w, add
}
