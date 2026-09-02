package ir

import (
	"math"
	"runtime"
	"sync"
	"testing"
)

// evalMagicS32 is the emitted sequence, written out in Go. It is what
// DeriveMagicS32's contract promises, so a divergence between this and
// `x / d` is a bug in the derivation rather than in the lowering.
func evalMagicS32(x int32, mg MagicS32) int32 {
	q := int32(int64(x) * int64(mg.M) >> 32)
	if mg.Add {
		q += x
	}
	if mg.Sub {
		q -= x
	}
	q >>= mg.S
	return q + int32(uint32(q)>>31)
}

func evalMagicU32(x uint32, mg MagicU32) uint32 {
	h := uint32(uint64(x) * uint64(mg.M) >> 32)
	if !mg.Add {
		return h >> mg.S
	}
	return (((x - h) >> 1) + h) >> (mg.S - 1)
}

// magicEligibleS reports whether d is a divisor DeriveMagicS32 is defined for.
func magicEligibleS(d int32) bool {
	if d == 0 || d == 1 || d == -1 || d == math.MinInt32 {
		return false
	}
	ad := uint32(d)
	if d < 0 {
		ad = uint32(-d)
	}
	return ad&(ad-1) != 0
}

func magicEligibleU(d uint32) bool { return d >= 2 && d&(d-1) != 0 }

// dividendProbes is the boundary set every divisor is checked against: the
// extremes, the values either side of them, and a stride across the range.
func dividendProbes() []int32 {
	xs := []int32{0, 1, -1, 2, -2, 3, -3, 7, -7, 100, -100, 12345, -12345,
		math.MaxInt32, math.MinInt32, math.MaxInt32 - 1, math.MinInt32 + 1,
		1 << 16, -(1 << 16), 65535, -65535, 1 << 30, -(1 << 30)}
	for i := int32(-70000); i <= 70000; i += 997 {
		xs = append(xs, i)
	}
	return xs
}

// TestMagicS32DenseDivisors walks every eligible divisor in a dense band around
// zero and checks the derived reciprocal against the boundary dividend set.
func TestMagicS32DenseDivisors(t *testing.T) {
	xs := dividendProbes()
	nAdd, nSub := 0, 0
	for d := int64(-70000); d <= 70000; d++ {
		dd := int32(d)
		if !magicEligibleS(dd) {
			continue
		}
		mg := DeriveMagicS32(dd)
		if mg.Add {
			nAdd++
		}
		if mg.Sub {
			nSub++
		}
		if mg.Add && mg.Sub {
			t.Fatalf("d=%d: both Add and Sub set", dd)
		}
		if mg.S >= 32 {
			t.Fatalf("d=%d: shift %d does not fit an i32 shift (mod-32)", dd, mg.S)
		}
		for _, x := range xs {
			if got, want := evalMagicS32(x, mg), x/dd; got != want {
				t.Fatalf("d=%d x=%d: got %d, want %d (M=%d S=%d add=%v sub=%v)",
					dd, x, got, want, mg.M, mg.S, mg.Add, mg.Sub)
			}
		}
	}
	// Both fixups have to be exercised, else the dense band proves nothing
	// about the arms the emitter has to get right.
	if nAdd == 0 || nSub == 0 {
		t.Errorf("fixup arms unexercised: add=%d sub=%d", nAdd, nSub)
	}
}

func TestMagicU32DenseDivisors(t *testing.T) {
	xs := []uint32{0, 1, 2, 3, 7, 100, 12345, 65535, 1 << 16, 1<<31 - 1,
		1 << 31, math.MaxUint32, math.MaxUint32 - 1}
	for i := uint32(0); i < 140000; i += 991 {
		xs = append(xs, i, math.MaxUint32-i)
	}
	nAdd := 0
	for d := uint64(2); d <= 140000; d++ {
		du := uint32(d)
		if !magicEligibleU(du) {
			continue
		}
		mg := DeriveMagicU32(du)
		eff := mg.S
		if mg.Add {
			nAdd++
			eff = mg.S - 1
		}
		if eff >= 32 {
			t.Fatalf("d=%d: effective shift %d does not fit an i32 shift (mod-32)", du, eff)
		}
		for _, x := range xs {
			if got, want := evalMagicU32(x, mg), x/du; got != want {
				t.Fatalf("d=%d x=%d: got %d, want %d (M=%d S=%d add=%v)",
					du, x, got, want, mg.M, mg.S, mg.Add)
			}
		}
	}
	if nAdd == 0 {
		t.Error("the 33-bit-magic arm went unexercised")
	}
}

// TestMagicShiftFitsI32 checks the shift bound where the large shifts live:
// the two ends of each range, plus a stride across the whole of it. An i32
// shift is defined modulo 32 on wasm and both natives, so a shift of exactly
// 32 would be a no-op rather than a zeroing shift.
func TestMagicShiftFitsI32(t *testing.T) {
	maxS, maxEff := uint(0), uint(0)
	checkS := func(d int32) {
		if !magicEligibleS(d) {
			return
		}
		if s := DeriveMagicS32(d).S; s >= 32 {
			t.Fatalf("signed d=%d: shift %d", d, s)
		} else if s > maxS {
			maxS = s
		}
	}
	checkU := func(d uint32) {
		if !magicEligibleU(d) {
			return
		}
		mg := DeriveMagicU32(d)
		eff := mg.S
		if mg.Add {
			eff = mg.S - 1
		}
		if eff >= 32 {
			t.Fatalf("unsigned d=%d: effective shift %d (S=%d add=%v)", d, eff, mg.S, mg.Add)
		} else if eff > maxEff {
			maxEff = eff
		}
	}
	const band = 300000
	for d := int64(math.MinInt32); d < math.MinInt32+band; d++ {
		checkS(int32(d))
	}
	for d := int64(math.MaxInt32) - band; d <= math.MaxInt32; d++ {
		checkS(int32(d))
	}
	for d := int64(-band); d < band; d++ {
		checkS(int32(d))
	}
	for d := int64(math.MinInt32); d < math.MaxInt32; d += 7919 {
		checkS(int32(d))
	}
	for d := uint64(2); d < band; d++ {
		checkU(uint32(d))
	}
	for d := uint64(1<<31) - band; d < uint64(1<<31)+band; d++ {
		checkU(uint32(d))
	}
	for d := uint64(1<<32) - band; d < 1<<32; d++ {
		checkU(uint32(d))
	}
	for d := uint64(2); d < 1<<32; d += 7919 {
		checkU(uint32(d))
	}
	t.Logf("largest shift: signed %d, unsigned effective %d", maxS, maxEff)
}

// TestMagicExhaustiveDividends is the check with no gap in it: for one divisor
// per distinct code path, every one of the 2^32 dividends. Skipped under
// -short — it takes about a minute across the machine's cores.
func TestMagicExhaustiveDividends(t *testing.T) {
	if testing.Short() {
		t.Skip("2^32 dividends per divisor")
	}
	workers := runtime.NumCPU()
	// One divisor per arm: plain, Sub, Add, large-shift, tiny-magic, negative
	// with each fixup.
	for _, d := range []int32{3, -3, 7, -7, 641, 1000000, 715827883, -1234567} {
		mg := DeriveMagicS32(d)
		var wg sync.WaitGroup
		var once sync.Once
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			go func(w int) {
				defer wg.Done()
				for x := int64(math.MinInt32) + int64(w); x <= math.MaxInt32; x += int64(workers) {
					xi := int32(x)
					if got, want := evalMagicS32(xi, mg), xi/d; got != want {
						once.Do(func() {
							t.Errorf("signed d=%d x=%d: got %d, want %d", d, xi, got, want)
						})
						return
					}
				}
			}(w)
		}
		wg.Wait()
	}
	for _, d := range []uint32{3, 7, 641, 1000000, 0x80000001, 2863311531, 4294967291} {
		mg := DeriveMagicU32(d)
		var wg sync.WaitGroup
		var once sync.Once
		wg.Add(workers)
		for w := 0; w < workers; w++ {
			go func(w int) {
				defer wg.Done()
				for x := uint64(w); x <= math.MaxUint32; x += uint64(workers) {
					xu := uint32(x)
					if got, want := evalMagicU32(xu, mg), xu/d; got != want {
						once.Do(func() {
							t.Errorf("unsigned d=%d x=%d: got %d, want %d", d, xu, got, want)
						})
						return
					}
				}
			}(w)
		}
		wg.Wait()
	}
}
