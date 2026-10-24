package e2ecompiler

import "math/bits"

type magicS32 struct {
	M   int32
	S   uint
	Add bool
	Sub bool
}

type magicS64 struct {
	M   int64
	S   uint
	Add bool
	Sub bool
}

func deriveMagicS32(d int32) magicS32 {
	m, s, add, sub := deriveMagicSW[int32, uint32](d)
	return magicS32{M: m, S: s, Add: add, Sub: sub}
}

func deriveMagicS64(d int64) magicS64 {
	m, s, add, sub := deriveMagicSW[int64, uint64](d)
	return magicS64{M: m, S: s, Add: add, Sub: sub}
}

func deriveMagicSW[S ~int32 | ~int64, U ~uint32 | ~uint64](d S) (m S, sh uint, add, sub bool) {
	w := uint(bits.OnesCount64(uint64(^U(0))))
	ad := U(d)
	if d < 0 {
		ad = U(-d)
	}
	two := U(1) << (w - 1)
	t := two + U(d)>>(w-1)
	anc := t - 1 - t%ad

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
	return m, p - w, d > 0 && m < 0, d < 0 && m > 0
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
