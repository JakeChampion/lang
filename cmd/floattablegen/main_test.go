package main

import (
	"math/big"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	floatFern  = "../../internal/stdlib/std/float.fern"
	stringFern = "../../internal/stdlib/std/string.fern"
)

// The encoded tables ship as Fern STRING literals, so the alphabet has to
// survive a Fern string literal unescaped. Fern source is UTF-8 and the table
// is indexed by byte, so the alphabet must also stay below 0x80.
func TestAlphabetNeedsNoEscapes(t *testing.T) {
	seen := map[byte]bool{}
	for v := 0; v < 64; v++ {
		c := enc6(v)
		switch {
		case c < 0x20 || c >= 0x80:
			t.Errorf("enc6(%d) = %#x: outside printable ASCII", v, c)
		case c == '"' || c == '\\':
			t.Errorf("enc6(%d) = %q: needs escaping in a Fern string literal", v, c)
		}
		if seen[c] {
			t.Errorf("enc6(%d) = %q: duplicate alphabet character", v, c)
		}
		seen[c] = true
		if got := dec6(c); got != v {
			t.Errorf("dec6(enc6(%d)) = %d, want %d", v, got, v)
		}
	}
}

func TestEncodeWordRoundTrip(t *testing.T) {
	vals := []uint64{
		0, 1, 63, 64, 1 << 32, ^uint64(0), ^uint64(0) - 1,
		1 << 63, (1 << 63) | 1, 0x8000000000000000, 0xcccccccccccccccd,
	}
	r := rand.New(rand.NewSource(20260805))
	for i := 0; i < 100000; i++ {
		vals = append(vals, r.Uint64())
	}
	for _, v := range vals {
		s := EncodeWord(v)
		if len(s) != WordChars {
			t.Fatalf("EncodeWord(%d) has length %d, want %d", v, len(s), WordChars)
		}
		if got := DecodeWord(s); got != v {
			t.Fatalf("DecodeWord(EncodeWord(%d)) = %d", v, got)
		}
	}
}

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

// The committed float.fern must carry exactly what the generator produces, so
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

// Decode the literals straight out of the committed file the same way the Fern
// `_db_word` decoder does, and check every entry against the big.Int cache.
// This is the invariant the Fern port actually depends on.
func TestCommittedTablesDecodeToCache(t *testing.T) {
	src, err := os.ReadFile(floatFern)
	if err != nil {
		t.Fatal(err)
	}
	lit := func(fn string) string {
		re := regexp.MustCompile(`(?s)function ` + fn + `\(\): string \{\s*return "([^"]*)";`)
		m := re.FindStringSubmatch(string(src))
		if m == nil {
			t.Fatalf("could not find the %s table literal in %s", fn, floatFern)
		}
		return m[1]
	}

	t64 := lit("_db_cache64")
	want64 := Cache64()
	if len(t64) != len(want64)*2*WordChars {
		t.Fatalf("_db_cache64 has %d chars, want %d", len(t64), len(want64)*2*WordChars)
	}
	for i, e := range want64 {
		hi := DecodeWord(t64[i*2*WordChars:])
		lo := DecodeWord(t64[i*2*WordChars+WordChars:])
		if hi != e[0] || lo != e[1] {
			t.Fatalf("_db_cache64 k=%d: decoded {%#016x,%#016x}, want {%#016x,%#016x}",
				i+MinK64, hi, lo, e[0], e[1])
		}
	}

	t32 := lit("_db_cache32")
	want32 := Cache32()
	if len(t32) != len(want32)*WordChars {
		t.Fatalf("_db_cache32 has %d chars, want %d", len(t32), len(want32)*WordChars)
	}
	for i, e := range want32 {
		if got := DecodeWord(t32[i*WordChars:]); got != e {
			t.Fatalf("_db_cache32 k=%d: decoded %#016x, want %#016x", i+MinK32, got, e)
		}
	}
}

// The tables must stay string literals: a Fern array literal is executable
// code that would rebuild the table on every call.
func TestTablesAreStringsNotArrays(t *testing.T) {
	src, err := os.ReadFile(floatFern)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"_db_cache64", "_db_cache32"} {
		i := strings.Index(string(src), "function "+fn+"(): string {")
		if i < 0 {
			t.Errorf("%s is not declared as `function %s(): string`", fn, fn)
		}
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	for i, b := range blocks {
		if b.generate() != b.generate() {
			t.Errorf("blocks[%d] (%s) is not deterministic", i, b.begin)
		}
	}
}

// The Eisel-Lemire table rounds DOWN everywhere except the band where 5^|q|
// still fits in a 64-bit word, which rounds UP. That asymmetry is load-bearing
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

// Decode the Eisel-Lemire literal straight out of the committed string.fern
// the same way the Fern `_el_word` decoder does, and check every entry.
func TestCommittedPow5DecodesToTable(t *testing.T) {
	src, err := os.ReadFile(stringFern)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)function _el_pow5\(\): string \{\s*return "([^"]*)";`)
	m := re.FindStringSubmatch(string(src))
	if m == nil {
		t.Fatalf("could not find the _el_pow5 table literal in %s", stringFern)
	}
	lit := m[1]
	want := Pow5()
	if len(lit) != len(want)*2*WordChars {
		t.Fatalf("_el_pow5 has %d chars, want %d", len(lit), len(want)*2*WordChars)
	}
	for i, e := range want {
		hi := DecodeWord(lit[i*2*WordChars:])
		lo := DecodeWord(lit[i*2*WordChars+WordChars:])
		if hi != e[0] || lo != e[1] {
			t.Fatalf("_el_pow5 q=%d: decoded {%#016x,%#016x}, want {%#016x,%#016x}",
				i+MinQ, hi, lo, e[0], e[1])
		}
	}
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
