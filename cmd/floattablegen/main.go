// Command floattablegen generates the lookup tables that Fern's float
// conversion routines use, and rewrites them into the stdlib in place:
//
//	go run ./cmd/floattablegen internal/stdlib/std/f64.fern internal/stdlib/std/string.fern
//
// It handles two tables, one per direction of conversion:
//
//   - DRAGONBOX (binary→decimal, `std/f64.fern`): normalised powers of ten,
//     128-bit for f64 and 64-bit for f32.
//   - EISEL-LEMIRE (decimal→binary, `std/string.fern`): normalised 128-bit
//     powers of five.
//
// The two are NOT interchangeable even though 10^k and 5^k share a normalised
// significand: they round differently in the last bit (see Pow5Entry), and each
// algorithm's correctness depends on its own convention.
//
// Each file is rewritten between its own marker comments, so a file only needs
// the block it actually uses. main_test.go re-derives both tables with math/big
// and checks the committed literals decode back to them.
//
// The tables ship as `const` u64 arrays, which every backend lays out as static
// data: a lookup is one load and allocates nothing. A 128-bit entry is two
// consecutive words, high then low.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
)

// split128 renders a 128-bit value as its high then low 64-bit word.
func split128(v *big.Int) (hi, lo uint64) {
	mask := new(big.Int).SetUint64(^uint64(0))
	return new(big.Int).Rsh(v, 64).Uint64(), new(big.Int).And(v, mask).Uint64()
}

// ---------------------------------------------------------------- dragonbox

// Dragonbox cache index bounds, as derived in the reference implementation:
//
//	min_k = min(-floor_log10_pow2_minus_log10_4_over_3(max_exponent - significand_bits),
//	            -floor_log10_pow2(max_exponent - significand_bits) + kappa)
//	max_k = max(-floor_log10_pow2_minus_log10_4_over_3(min_exponent - significand_bits),
//	            -floor_log10_pow2(min_exponent - significand_bits) + kappa)
const (
	MinK64, MaxK64 = -292, 326
	MinK32, MaxK32 = -31, 46
)

// FloorLog2Pow10 is floor(log2(10^e)), exact over the exponent range Dragonbox
// uses. Mirrors `_db_log2_pow10` in the Fern port.
func FloorLog2Pow10(e int) int { return (e * 1741647) >> 19 }

// CacheEntry is ceil(10^k * 2^(width-1-floor_log2_pow10(k))): 10^k normalised
// so its most significant bit is the top bit of a 128-bit (binary64) or 64-bit
// (binary32) word, rounded UP for every k. Bit-for-bit the table published in
// jk-jeon/dragonbox's dragonbox.h.
func CacheEntry(k, width int) *big.Int {
	shift := width - 1 - FloorLog2Pow10(k)
	num, den := big.NewInt(1), big.NewInt(1)
	ten, two := big.NewInt(10), big.NewInt(2)
	if k >= 0 {
		num.Exp(ten, big.NewInt(int64(k)), nil)
	} else {
		den.Exp(ten, big.NewInt(int64(-k)), nil)
	}
	if shift >= 0 {
		num.Mul(num, new(big.Int).Exp(two, big.NewInt(int64(shift)), nil))
	} else {
		den.Mul(den, new(big.Int).Exp(two, big.NewInt(int64(-shift)), nil))
	}
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// Cache64 returns the binary64 table: {high, low} 64-bit halves of the 128-bit
// entry, for k = MinK64..MaxK64.
func Cache64() [][2]uint64 {
	out := make([][2]uint64, 0, MaxK64-MinK64+1)
	for k := MinK64; k <= MaxK64; k++ {
		hi, lo := split128(CacheEntry(k, 128))
		out = append(out, [2]uint64{hi, lo})
	}
	return out
}

// Cache32 returns the binary32 table: the 64-bit entry for k = MinK32..MaxK32.
func Cache32() []uint64 {
	out := make([]uint64, 0, MaxK32-MinK32+1)
	for k := MinK32; k <= MaxK32; k++ {
		out = append(out, CacheEntry(k, 64).Uint64())
	}
	return out
}

// ---------------------------------------------------------------- eisel-lemire

// Eisel-Lemire power-of-five bounds: q spans every decimal exponent that can
// turn a <=19-digit significand into a finite non-zero binary64.
const (
	MinQ = -342
	MaxQ = 308

	// Pow5CeilBand is the low end of the negative-q band that rounds UP
	// instead of down — exactly the q where 5^|q| still fits in a 64-bit word
	// (5^27 < 2^64 <= 5^28).
	Pow5CeilBand = -27
)

// Pow5Entry is 5^q normalised into [2^127, 2^128).
//
// Rounding is TRUNCATION (floor) everywhere EXCEPT q in [Pow5CeilBand, -1],
// which round UP. That band is where 5^|q| still fits in a 64-bit word, and
// rounding up there is what keeps "low <= 1" a sound signal for "the dropped
// bits were all zero" — the test Eisel-Lemire's round-half-to-even branch keys
// off. This convention was derived by checking all 651 entries against the
// upstream fast_float table, not from its one-line description, which does not
// mention the band.
//
// NOTE this differs from Dragonbox's CacheEntry, which rounds up at every
// index. 10^k and 5^k have the same normalised significand, so the two tables
// agree on every bit but the last — and that last bit is essential for both
// algorithms, so they cannot be shared.
func Pow5Entry(q int) *big.Int {
	num, den := big.NewInt(1), big.NewInt(1)
	five := big.NewInt(5)
	if q >= 0 {
		num.Exp(five, big.NewInt(int64(q)), nil)
	} else {
		den.Exp(five, big.NewInt(int64(-q)), nil)
	}
	lo := new(big.Int).Lsh(big.NewInt(1), 127)
	hi := new(big.Int).Lsh(big.NewInt(1), 128)
	for new(big.Int).Quo(num, den).Cmp(lo) < 0 {
		num.Lsh(num, 1)
	}
	for new(big.Int).Quo(num, den).Cmp(hi) >= 0 {
		den.Lsh(den, 1)
	}
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 && q < 0 && q >= Pow5CeilBand {
		quo.Add(quo, big.NewInt(1))
	}
	return quo
}

// Pow5 returns the Eisel-Lemire table: {high, low} halves for q = MinQ..MaxQ.
func Pow5() [][2]uint64 {
	out := make([][2]uint64, 0, MaxQ-MinQ+1)
	for q := MinQ; q <= MaxQ; q++ {
		hi, lo := split128(Pow5Entry(q))
		out = append(out, [2]uint64{hi, lo})
	}
	return out
}

// flat lays out 128-bit entries as consecutive high and low words.
func flat(t [][2]uint64) []uint64 {
	out := make([]uint64, 0, 2*len(t))
	for _, e := range t {
		out = append(out, e[0], e[1])
	}
	return out
}

// ArrayLiteral renders words as the elements of a Fern u64 array literal,
// `per` to a line.
func ArrayLiteral(words []uint64, per int) string {
	var b strings.Builder
	for i, w := range words {
		if i%per == 0 {
			b.WriteString("\n    ")
		} else {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "0x%016xu64,", w)
	}
	b.WriteString("\n")
	return b.String()
}

// ---------------------------------------------------------------- emit

type block struct {
	begin, end string
	generate   func() string
}

// blocks are the marker-delimited regions this command owns. A source file
// carries only the blocks it needs; the rest are left alone.
var blocks = []block{
	{
		begin: "// BEGIN GENERATED DRAGONBOX TABLES (cmd/floattablegen) — do not edit by hand.",
		end:   "// END GENERATED DRAGONBOX TABLES",
		generate: func() string {
			return fmt.Sprintf(`// Dragonbox 128-bit power-of-ten cache for f64: k = %d..%d (%d entries),
// the high 64 bits of each, then the low.
const DB_CACHE64: u64[] = [%s];
// Dragonbox 64-bit power-of-ten cache for f32: k = %d..%d (%d entries).
const DB_CACHE32: u64[] = [%s];
`, MinK64, MaxK64, MaxK64-MinK64+1, ArrayLiteral(flat(Cache64()), 2),
				MinK32, MaxK32, MaxK32-MinK32+1, ArrayLiteral(Cache32(), 2))
		},
	},
	{
		begin: "// BEGIN GENERATED EISEL-LEMIRE TABLE (cmd/floattablegen) — do not edit by hand.",
		end:   "// END GENERATED EISEL-LEMIRE TABLE",
		generate: func() string {
			return fmt.Sprintf(`// Eisel-Lemire 128-bit power-of-five table: q = %d..%d (%d entries),
// the high 64 bits of each, then the low.
const EL_POW5: u64[] = [%s];
`, MinQ, MaxQ, MaxQ-MinQ+1, ArrayLiteral(flat(Pow5()), 2))
		},
	},
}

// Rewrite replaces every marked table block present in `src` with freshly
// generated content, leaving files that carry neither block untouched. It
// reports an error if a begin marker has no matching end marker.
func Rewrite(src string) (string, error) {
	found := false
	for _, b := range blocks {
		i := strings.Index(src, b.begin)
		if i < 0 {
			continue
		}
		j := strings.Index(src, b.end)
		if j < i {
			return "", fmt.Errorf("end marker %q not found after its begin marker", b.end)
		}
		src = src[:i] + b.begin + "\n" + b.generate() + src[j:]
		found = true
	}
	if !found {
		return src, nil
	}
	return fmtsource.Format(src)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: floattablegen <file.fern>...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out, err := Rewrite(string(src))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		if out == string(src) {
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
