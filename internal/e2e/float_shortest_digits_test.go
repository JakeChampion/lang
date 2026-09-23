package e2e

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// shortestDigitsWant is (significand, exponent) for Go's shortest
// round-trip rendering of v at the given width: the digits of the
// mantissa with the point and trailing zeros dropped, and the exponent
// moved down by the digits that followed the point.
func shortestDigitsWant(v float64, bits int) (string, int) {
	s := strconv.FormatFloat(v, 'e', -1, bits)
	mant, exps, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(exps)
	digits := strings.TrimRight(strings.Replace(mant, ".", "", 1), "0")
	return digits, exp - (len(digits) - 1)
}

// TestFloatShortestDigits pins `shortest_digits()` on f64 and f32 to
// strconv's shortest digits: the same Dragonbox result `to_string()`
// renders, handed out as a pair, with zero, the infinities and NaN
// answering (0, 0) and the sign dropped.
func TestFloatShortestDigits(t *testing.T) {
	vals := []float64{
		0.1, 100, 1.5, -1.5, 1e15, 1e16, 1e17, 123456789012345680, 0.3, 1e-5,
		math.Ldexp(1, -24), math.Ldexp(1, -25), math.Ldexp(1, -1022),
		2.2250738585072009e-308, 1e-310, 5e-324, math.MaxFloat64,
		1 << 53, 1<<53 + 2, 1.0 / 3, 0.1 + 0.2, 9.999999999999999e22,
	}
	r := rand.New(rand.NewSource(20260922))
	for len(vals) < 80 {
		v := math.Float64frombits(r.Uint64())
		if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
			continue
		}
		vals = append(vals, v)
	}
	var b strings.Builder
	b.WriteString("import \"std/float\";\nfunction show(d: (i64, i32)): void {\n")
	b.WriteString("    write(d.0.to_string()); write(\" \"); write(d.1.to_string()); write(\"\\n\");\n}\n")
	b.WriteString("function main(): i32 {\n")
	want := []string{}
	for _, v := range vals {
		b.WriteString(fmt.Sprintf("    show(f64_from_bits(%d).shortest_digits());\n", int64(math.Float64bits(v))))
		d, e := shortestDigitsWant(math.Abs(v), 64)
		want = append(want, fmt.Sprintf("%s %d", d, e))
		f := float32(v)
		if math.IsInf(float64(f), 0) || f == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("    show(f32_from_bits(%d).shortest_digits());\n", int32(math.Float32bits(f))))
		d, e = shortestDigitsWant(math.Abs(float64(f)), 32)
		want = append(want, fmt.Sprintf("%s %d", d, e))
	}
	for _, lit := range []string{"0.0", "0.0 - 0.0", "1.0 / 0.0", "0.0 - 1.0 / 0.0", "0.0 / 0.0"} {
		b.WriteString(fmt.Sprintf("    show((%s).shortest_digits());\n    show(((%s) as f32).shortest_digits());\n", lit, lit))
		want = append(want, "0 0", "0 0")
	}
	b.WriteString("    return 0;\n}\n")

	out, code := compileAndRunX86_64(t, b.String())
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d\noutput:\n%s", len(got), len(want), out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestFloatPow10Table pins the power-of-ten words `std/float` exports
// to the exact definition: with l2 = floor(log2(10^k)), the 128-bit
// value ceil(10^k × 2^(127 - l2)), high word first, and `pow10_log2` is
// l2 itself. Every entry is checked, both ends of the range included.
func TestFloatPow10Table(t *testing.T) {
	var b strings.Builder
	b.WriteString("import \"std/float\";\nfunction main(): i32 {\n    var k: i32 = 0 - 292;\n    while (k <= 326) {\n")
	b.WriteString("        write(float.pow10_hi(k).to_string()); write(\" \"); write(float.pow10_lo(k).to_string()); write(\" \"); write(float.pow10_log2(k).to_string()); write(\"\\n\");\n")
	b.WriteString("        k = k + 1;\n    }\n    return 0;\n}\n")
	out, code := compileAndRunX86_64(t, b.String())
	if code != 0 {
		t.Fatalf("exit = %d\noutput:\n%s", code, out)
	}
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != 619 {
		t.Fatalf("got %d lines, want 619", len(got))
	}
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	for i, line := range got {
		k := i - 292
		var c *big.Int
		var l2 int
		if k >= 0 {
			v := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)
			l2 = v.BitLen() - 1
			if 127-l2 >= 0 {
				c = new(big.Int).Lsh(v, uint(127-l2))
			} else {
				d := new(big.Int).Lsh(big.NewInt(1), uint(l2-127))
				q, m := new(big.Int).QuoRem(v, d, new(big.Int))
				if m.Sign() != 0 {
					q.Add(q, big.NewInt(1))
				}
				c = q
			}
		} else {
			den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-k)), nil)
			l2 = -den.BitLen()
			num := new(big.Int).Lsh(big.NewInt(1), uint(127-l2))
			q, m := new(big.Int).QuoRem(num, den, new(big.Int))
			if m.Sign() != 0 {
				q.Add(q, big.NewInt(1))
			}
			c = q
		}
		hi := new(big.Int).Rsh(c, 64)
		lo := new(big.Int).And(c, mask)
		want := fmt.Sprintf("%s %s %d", hi, lo, l2)
		if line != want {
			t.Errorf("k=%d: got %q, want %q", k, line, want)
		}
	}
}
