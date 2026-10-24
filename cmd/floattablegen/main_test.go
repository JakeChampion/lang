package main

import (
	"math/big"
	"os"
	"regexp"
	"strconv"
	"testing"
)

const (
	floatFern  = "../../internal/stdlib/std/f64.fern.md"
	stringFern = "../../internal/stdlib/std/string.fern.md"
)

func TestCacheEntryIsNormalisedCeiling(t *testing.T) {
	for _, tc := range []struct{ minK, maxK, width int }{
		{MinK64, MaxK64, 128},
		{MinK32, MaxK32, 64},
	} {
		for k := tc.minK; k <= tc.maxK; k++ {
			v := CacheEntry(k, tc.width)
			if v.BitLen() != tc.width {
				t.Fatalf("width %d, k=%d: entry has %d bits, want exactly %d (top bit must be set)",
					tc.width, k, v.BitLen(), tc.width)
			}
			shift := tc.width - 1 - FloorLog2Pow10(k)
			exact := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(k))), nil))
			if k < 0 {
				exact.Inv(exact)
			}
			p2 := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(2), big.NewInt(int64(abs(shift))), nil))
			if shift < 0 {
				p2.Inv(p2)
			}
			exact.Mul(exact, p2)
			floor := new(big.Int).Quo(exact.Num(), exact.Denom())
			want := new(big.Int).Set(floor)
			if new(big.Int).Mul(floor, exact.Denom()).Cmp(exact.Num()) != 0 {
				want.Add(want, big.NewInt(1))
			}
			if v.Cmp(want) != 0 {
				t.Fatalf("width %d, k=%d: CacheEntry = %s, want ceil = %s", tc.width, k, v, want)
			}
		}
	}
	if got, want := CacheEntry(0, 128), new(big.Int).Lsh(big.NewInt(1), 127); got.Cmp(want) != 0 {
		t.Errorf("CacheEntry(0,128) = %s, want 2^127", got)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestCommittedFileIsUpToDate(t *testing.T) {
	for _, path := range []string{floatFern, stringFern} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Rewrite(string(src))
		if err != nil {
			t.Fatal(err)
		}
		if out != string(src) {
			t.Errorf("%s is out of date; regenerate with:\n\tgo run ./cmd/floattablegen %s %s",
				path, floatFern, stringFern)
		}
	}
}

func committedTable(t *testing.T, path, name string) []uint64 {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)const ` + name + `: u64\[\] = \[(.*?)\];`)
	m := re.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatalf("could not find `const %s: u64[]` in %s", name, path)
	}
	var out []uint64
	for _, w := range regexp.MustCompile(`0x([0-9a-f]+)u64`).FindAllStringSubmatch(m[1], -1) {
		v, err := strconv.ParseUint(w[1], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func checkTable(t *testing.T, name string, got, want []uint64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s has %d words, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %#016x, want %#016x", name, i, got[i], want[i])
		}
	}
}

func TestCommittedTablesMatchCache(t *testing.T) {
	checkTable(t, "DB_CACHE64", committedTable(t, floatFern, "DB_CACHE64"), flat(Cache64()))
	checkTable(t, "DB_CACHE32", committedTable(t, floatFern, "DB_CACHE32"), Cache32())
}

func TestGenerateIsDeterministic(t *testing.T) {
	for i, b := range blocks {
		if b.generate() != b.generate() {
			t.Errorf("blocks[%d] (%s) is not deterministic", i, b.begin)
		}
	}
}

func TestPow5RoundingConvention(t *testing.T) {
	two127 := new(big.Int).Lsh(big.NewInt(1), 127)
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	for q := MinQ; q <= MaxQ; q++ {
		got := Pow5Entry(q)
		if got.Cmp(two127) < 0 || got.Cmp(two128) >= 0 {
			t.Fatalf("q=%d: entry is not normalised into [2^127, 2^128)", q)
		}
		num, den := big.NewInt(1), big.NewInt(1)
		if q >= 0 {
			num.Exp(big.NewInt(5), big.NewInt(int64(q)), nil)
		} else {
			den.Exp(big.NewInt(5), big.NewInt(int64(-q)), nil)
		}
		for new(big.Int).Quo(num, den).Cmp(two127) < 0 {
			num.Lsh(num, 1)
		}
		for new(big.Int).Quo(num, den).Cmp(two128) >= 0 {
			den.Lsh(den, 1)
		}
		floor, rem := new(big.Int).QuoRem(num, den, new(big.Int))
		want := new(big.Int).Set(floor)
		roundsUp := rem.Sign() != 0 && q < 0 && q >= Pow5CeilBand
		if roundsUp {
			want.Add(want, big.NewInt(1))
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("q=%d: Pow5Entry = %s, want %s (roundsUp=%v)", q, got, want, roundsUp)
		}
	}
	if fits := new(big.Int).Exp(big.NewInt(5), big.NewInt(27), nil); fits.BitLen() > 64 {
		t.Errorf("5^27 should still fit in 64 bits (BitLen=%d)", fits.BitLen())
	}
	if over := new(big.Int).Exp(big.NewInt(5), big.NewInt(28), nil); over.BitLen() <= 64 {
		t.Errorf("5^28 should exceed 64 bits (BitLen=%d)", over.BitLen())
	}
}

func TestCommittedPow5MatchesTable(t *testing.T) {
	checkTable(t, "EL_POW5", committedTable(t, stringFern, "EL_POW5"), flat(Pow5()))
}

func TestDragonboxAndPow5TablesDiffer(t *testing.T) {
	same, diff := 0, 0
	for k := MinK64; k <= MaxK64; k++ {
		if k < MinQ || k > MaxQ {
			continue
		}
		if CacheEntry(k, 128).Cmp(Pow5Entry(k)) == 0 {
			same++
		} else {
			diff++
		}
	}
	if diff == 0 {
		t.Error("the Dragonbox and Eisel-Lemire tables are identical over their shared range; " +
			"one of the rounding conventions is wrong")
	}
	t.Logf("over the shared index range: %d entries identical, %d differ", same, diff)
}
