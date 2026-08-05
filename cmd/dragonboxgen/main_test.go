package main

import (
	"math/big"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"
)

const floatFern = "../../internal/stdlib/std/float.fern"

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
	src, err := os.ReadFile(floatFern)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Rewrite(string(src))
	if err != nil {
		t.Fatal(err)
	}
	if out != string(src) {
		t.Errorf("%s is out of date; regenerate with:\n\tgo run ./cmd/dragonboxgen %s", floatFern, floatFern)
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
	if Generate() != Generate() {
		t.Error("Generate() is not deterministic")
	}
}
