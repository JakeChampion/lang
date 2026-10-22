package main

import (
	"math/big"
	"os"
	"regexp"
	"strconv"
	"testing"
)

const (
	floatFern  = "../../internal/stdlib/std/f64.fern"
	stringFern = "../../internal/stdlib/std/string.fern"
)

// The cache entry must be the ceiling of the exact rational 10^k * 2^shift,
// and must be normalised (top bit set) so Dragonbox's fixed shift amounts land
// where it expects.
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
			// Re-derive independently with big.Rat and check ceiling.
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
	// k = 0 is exactly 2^(width-1).
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

// The committed f64.fern must carry exactly what the generator produces, so
// regenerating is a no-op.
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

// committedTable reads the elements of `const name: u64[]` out of a committed
// Fern source.
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

// Every word of the committed Dragonbox tables is the big.Int cache's.
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

// The Eisel-Lemire table rounds DOWN everywhere except the band where 5^|q|
// still fits in a 64-bit word, which rounds UP. That asymmetry is essential
// — it is what makes "low <= 1" a sound tie signal — and it is exactly what
// distinguishes this table from the Dragonbox one, so pin it directly.
func TestPow5RoundingConvention(t *testing.T) {
	two127 := new(big.Int).Lsh(big.NewInt(1), 127)
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	for q := MinQ; q <= MaxQ; q++ {
		got := Pow5Entry(q)
		if got.Cmp(two127) < 0 || got.Cmp(two128) >= 0 {
			t.Fatalf("q=%d: entry is not normalised into [2^127, 2^128)", q)
		}
		// Re-derive the exact rational 5^q * 2^s and take both roundings.
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
	// The band boundary is where 5^|q| stops fitting in 64 bits.
	if fits := new(big.Int).Exp(big.NewInt(5), big.NewInt(27), nil); fits.BitLen() > 64 {
		t.Errorf("5^27 should still fit in 64 bits (BitLen=%d)", fits.BitLen())
	}
	if over := new(big.Int).Exp(big.NewInt(5), big.NewInt(28), nil); over.BitLen() <= 64 {
		t.Errorf("5^28 should exceed 64 bits (BitLen=%d)", over.BitLen())
	}
}

// Every word of the committed Eisel-Lemire table is the big.Int table's.
func TestCommittedPow5MatchesTable(t *testing.T) {
	checkTable(t, "EL_POW5", committedTable(t, stringFern, "EL_POW5"), flat(Pow5()))
}

// The two tables must NOT be accidentally unified: they agree on their leading
// bits but differ in the last one, and each algorithm depends on its own
// convention.
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
