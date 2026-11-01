package e2ecompiler

import "math/bits"

type magicS32 struct {
	M int64
	S uint
}

type magicS64 struct {
	M   int64
	S   uint
	Add bool
	Sub bool
}

// deriveMagicS32 is the one-multiply form for a signed 32-bit divisor:
// s = 31 + ceil(log2 |d|), m = sign(d) * (floor(2^s / |d|) + 1).
func deriveMagicS32(d int32) magicS32 {
	ad := int64(d)
	if ad < 0 {
		ad = -ad
	}
	s := uint(31 + bits.Len64(uint64(ad-1)))
	m := int64(1)<<s/ad + 1
	if d < 0 {
		m = -m
	}
	return magicS32{M: m, S: s}
}

func deriveMagicS64(d int64) magicS64 {
	ad := uint64(d)
	if d < 0 {
		ad = uint64(-d)
	}
	const w = 64
	two := uint64(1) << (w - 1)
	t := two + uint64(d)>>(w-1)
	anc := t - 1 - t%ad

	p := uint(w - 1)
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

	m := int64(q2 + 1)
	if d < 0 {
		m = -m
	}
	return magicS64{M: m, S: p - w, Add: d > 0 && m < 0, Sub: d < 0 && m > 0}
}

type magicU32 struct {
	M   uint32
	S   uint
	Add bool
}

type magicU64 struct {
	M   uint64
	S   uint
	Add bool
}

func deriveMagicU32(d uint32) magicU32 {
	m, s, add := deriveMagicUW(d)
	return magicU32{M: m, S: s, Add: add}
}

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
