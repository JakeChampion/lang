// Command dragonboxgen generates the Dragonbox power-of-ten cache tables that
// `internal/stdlib/std/float.fern` uses for shortest round-trip float
// formatting, and rewrites them into that file in place:
//
//	go run ./cmd/dragonboxgen internal/stdlib/std/float.fern
//
// The tables ship as Fern STRING literals rather than array literals: a string
// literal is static rodata, so a lookup allocates nothing and no table is ever
// "built" at run time. (A Fern array literal is executable code — see the same
// note in std/unicode.fern.) Each 64-bit word is 11 characters of a
// 64-character alphabet, most significant digit first.
//
// The cache entry for a decimal exponent k is
//
//	ceil(10^k * 2^(bits-1-floor_log2_pow10(k)))
//
// i.e. 10^k normalised so its most significant bit is the top bit of a 128-bit
// (binary64) or 64-bit (binary32) word, rounded up. This is bit-for-bit the
// table published in jk-jeon/dragonbox's dragonbox.h; main_test.go re-derives
// it with math/big and checks the committed literals decode back to it.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"
)

// Dragonbox cache index bounds, as derived in the reference implementation:
//
//	min_k = min(-floor_log10_pow2_minus_log10_4_over_3(max_exponent - significand_bits),
//	            -floor_log10_pow2(max_exponent - significand_bits) + kappa)
//	max_k = max(-floor_log10_pow2_minus_log10_4_over_3(min_exponent - significand_bits),
//	            -floor_log10_pow2(min_exponent - significand_bits) + kappa)
const (
	MinK64, MaxK64 = -292, 326
	MinK32, MaxK32 = -31, 46

	// WordChars is the number of alphabet characters per 64-bit word:
	// ceil(64/6) == 11.
	WordChars = 11
)

// FloorLog2Pow10 is floor(log2(10^e)), exact over the exponent range Dragonbox
// uses. Mirrors `floor_log2_pow10` in the Fern port.
func FloorLog2Pow10(e int) int { return (e * 1741647) >> 19 }

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

// DecodeWord is the inverse of EncodeWord, and mirrors `_db_word` in the Fern
// port exactly (accumulate v*64 + digit, most significant digit first).
func DecodeWord(s string) uint64 {
	var v uint64
	for i := 0; i < WordChars; i++ {
		v = v*64 + uint64(dec6(s[i]))
	}
	return v
}

// ---------------------------------------------------------------- cache

// CacheEntry is ceil(10^k * 2^(width-1-floor_log2_pow10(k))), the normalised
// power of ten Dragonbox multiplies by.
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
	mask := new(big.Int).SetUint64(^uint64(0))
	out := make([][2]uint64, 0, MaxK64-MinK64+1)
	for k := MinK64; k <= MaxK64; k++ {
		v := CacheEntry(k, 128)
		hi := new(big.Int).Rsh(v, 64).Uint64()
		lo := new(big.Int).And(v, mask).Uint64()
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

// ---------------------------------------------------------------- emit

const (
	beginMarker = "// BEGIN GENERATED DRAGONBOX TABLES (cmd/dragonboxgen) — do not edit by hand."
	endMarker   = "// END GENERATED DRAGONBOX TABLES"
)

// Generate returns the Fern source block, marker comments included, that
// carries the two cache tables.
func Generate() string {
	var b strings.Builder
	b.WriteString(beginMarker)
	b.WriteString("\n")
	fmt.Fprintf(&b, `// Dragonbox 128-bit power-of-ten cache for f64: k = %d..%d (%d entries),
// 22 characters each — 11 for the high 64 bits, then 11 for the low.
func_db_cache64_placeholder
// Dragonbox 64-bit power-of-ten cache for f32: k = %d..%d (%d entries),
// 11 characters each.
func_db_cache32_placeholder
`, MinK64, MaxK64, MaxK64-MinK64+1, MinK32, MaxK32, MaxK32-MinK32+1)
	src := b.String()
	src = strings.Replace(src, "func_db_cache64_placeholder",
		"function _db_cache64(): string {\n    return \""+EncodeCache64()+"\";\n}", 1)
	src = strings.Replace(src, "func_db_cache32_placeholder",
		"function _db_cache32(): string {\n    return \""+EncodeCache32()+"\";\n}", 1)
	return src + endMarker
}

// Rewrite replaces the marked table block in `src` with freshly generated
// tables. It reports an error if the markers are missing or out of order.
func Rewrite(src string) (string, error) {
	i := strings.Index(src, beginMarker)
	if i < 0 {
		return "", fmt.Errorf("begin marker %q not found", beginMarker)
	}
	j := strings.Index(src, endMarker)
	if j < i {
		return "", fmt.Errorf("end marker %q not found after begin marker", endMarker)
	}
	return src[:i] + Generate() + src[j+len(endMarker):], nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dragonboxgen <path-to-float.fern>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := Rewrite(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
