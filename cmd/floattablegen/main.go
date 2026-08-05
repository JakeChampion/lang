// Command floattablegen generates the lookup tables that Fern's float
// conversion routines use, and rewrites them into the stdlib in place:
//
//	go run ./cmd/floattablegen internal/stdlib/std/float.fern internal/stdlib/std/string.fern
//
// It handles two tables, one per direction of conversion:
//
//   - DRAGONBOX (binary→decimal, `std/float.fern`): normalised powers of ten,
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
// The tables ship as Fern STRING literals rather than array literals: a string
// literal is static rodata, so a lookup allocates nothing and no table is ever
// "built" at run time (a Fern array literal is executable code — see the same
// note in std/unicode.fern). Each 64-bit word is 11 characters of a
// 64-character alphabet, most significant digit first. Unused tables are
// dead-code eliminated, so a program that never converts floats does not carry
// them.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"
)

// ---------------------------------------------------------------- alphabet

// The alphabet is 64 printable ASCII characters in two contiguous spans,
// '0'..'[' (48..91) and ']'..'p' (93..112). It skips '\\' (92) and stays
// clear of '"' (34), so no encoded character ever needs an escape inside a
// Fern string literal, and every character is a single UTF-8 byte.
func enc6(v int) byte {
	if v < 44 {
		return byte(48 + v)
	}
	return byte(49 + v)
}

func dec6(c byte) int {
	if c < 92 {
		return int(c) - 48
	}
	return int(c) - 49
}

// WordChars is the number of alphabet characters per 64-bit word: ceil(64/6).
const WordChars = 11

// EncodeWord renders a 64-bit word as WordChars alphabet characters, most
// significant base-64 digit first.
func EncodeWord(v uint64) string {
	var b [WordChars]byte
	for i := WordChars - 1; i >= 0; i-- {
		b[i] = enc6(int(v & 63))
		v >>= 6
	}
	return string(b[:])
}

// DecodeWord is the inverse of EncodeWord. It mirrors the Fern decoders
// (`_db_word` / `_el_word`) exactly: accumulate v*64 + digit, most significant
// digit first.
func DecodeWord(s string) uint64 {
	var v uint64
	for i := 0; i < WordChars; i++ {
		v = v*64 + uint64(dec6(s[i]))
	}
	return v
}

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

// EncodeCache64 renders the binary64 table as 22 characters per entry: 11 for
// the high half then 11 for the low half.
func EncodeCache64() string {
	var b strings.Builder
	for _, e := range Cache64() {
		b.WriteString(EncodeWord(e[0]))
		b.WriteString(EncodeWord(e[1]))
	}
	return b.String()
}

// EncodeCache32 renders the binary32 table as 11 characters per entry.
func EncodeCache32() string {
	var b strings.Builder
	for _, e := range Cache32() {
		b.WriteString(EncodeWord(e))
	}
	return b.String()
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
// agree on every bit but the last — and that last bit is load-bearing for both
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

// EncodePow5 renders the power-of-five table as 22 characters per entry.
func EncodePow5() string {
	var b strings.Builder
	for _, e := range Pow5() {
		b.WriteString(EncodeWord(e[0]))
		b.WriteString(EncodeWord(e[1]))
	}
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
// 22 characters each — 11 for the high 64 bits, then 11 for the low.
function _db_cache64(): string {
    return %q;
}
// Dragonbox 64-bit power-of-ten cache for f32: k = %d..%d (%d entries),
// 11 characters each.
function _db_cache32(): string {
    return %q;
}
`, MinK64, MaxK64, MaxK64-MinK64+1, EncodeCache64(),
				MinK32, MaxK32, MaxK32-MinK32+1, EncodeCache32())
		},
	},
	{
		begin: "// BEGIN GENERATED EISEL-LEMIRE TABLE (cmd/floattablegen) — do not edit by hand.",
		end:   "// END GENERATED EISEL-LEMIRE TABLE",
		generate: func() string {
			return fmt.Sprintf(`// Eisel-Lemire 128-bit power-of-five table: q = %d..%d (%d entries),
// 22 characters each — 11 for the high 64 bits, then 11 for the low.
function _el_pow5(): string {
    return %q;
}
`, MinQ, MaxQ, MaxQ-MinQ+1, EncodePow5())
		},
	},
}

// Rewrite replaces every marked table block present in `src` with freshly
// generated content, leaving files that carry neither block untouched. It
// reports an error if a begin marker has no matching end marker.
func Rewrite(src string) (string, error) {
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
	}
	return src, nil
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
