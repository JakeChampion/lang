package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/jakechampion/lang/internal/syntax/fmtsource"
)

func split128(v *big.Int) (hi, lo uint64) {
	mask := new(big.Int).SetUint64(^uint64(0))
	return new(big.Int).Rsh(v, 64).Uint64(), new(big.Int).And(v, mask).Uint64()
}

const (
	MinK64, MaxK64 = -292, 326
	MinK32, MaxK32 = -31, 46
)

func FloorLog2Pow10(e int) int { return (e * 1741647) >> 19 }

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

func Cache64() [][2]uint64 {
	out := make([][2]uint64, 0, MaxK64-MinK64+1)
	for k := MinK64; k <= MaxK64; k++ {
		hi, lo := split128(CacheEntry(k, 128))
		out = append(out, [2]uint64{hi, lo})
	}
	return out
}

func Cache32() []uint64 {
	out := make([]uint64, 0, MaxK32-MinK32+1)
	for k := MinK32; k <= MaxK32; k++ {
		out = append(out, CacheEntry(k, 64).Uint64())
	}
	return out
}

const (
	MinQ = -342
	MaxQ = 308

	Pow5CeilBand = -27
)

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

func Pow5() [][2]uint64 {
	out := make([][2]uint64, 0, MaxQ-MinQ+1)
	for q := MinQ; q <= MaxQ; q++ {
		hi, lo := split128(Pow5Entry(q))
		out = append(out, [2]uint64{hi, lo})
	}
	return out
}

func flat(t [][2]uint64) []uint64 {
	out := make([]uint64, 0, 2*len(t))
	for _, e := range t {
		out = append(out, e[0], e[1])
	}
	return out
}

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

type block struct {
	begin, end string
	generate   func() string
}

var blocks = []block{
	{
		begin: "// BEGIN GENERATED LONG DOUBLE CACHE (cmd/floattablegen)",
		end:   "// END GENERATED LONG DOUBLE CACHE",
		generate: func() string {
			return fmt.Sprintf("const POW10_CACHE: u64[] = [%s];\n", ArrayLiteral(flat(Cache64()), 2))
		},
	},
	{
		begin: "// BEGIN GENERATED DRAGONBOX TABLES (cmd/floattablegen) — do not edit by hand.",
		end:   "// END GENERATED DRAGONBOX TABLES",
		generate: func() string {
			return fmt.Sprintf(`// The f64 cache covers powers of ten from %d through %d with %d entries.
// Each entry stores the high 64 bits before the low 64 bits.
const DB_CACHE64: u64[] = [%s];
// The f32 cache covers powers of ten from %d through %d with %d entries.
const DB_CACHE32: u64[] = [%s];
`, MinK64, MaxK64, MaxK64-MinK64+1, ArrayLiteral(flat(Cache64()), 2),
				MinK32, MaxK32, MaxK32-MinK32+1, ArrayLiteral(Cache32(), 2))
		},
	},
	{
		begin: "// BEGIN GENERATED EISEL-LEMIRE TABLE (cmd/floattablegen) — do not edit by hand.",
		end:   "// END GENERATED EISEL-LEMIRE TABLE",
		generate: func() string {
			return fmt.Sprintf(`// The table covers powers of five from %d through %d with %d entries.
// Each entry stores the high 64 bits before the low 64 bits.
const EL_POW5: u64[] = [%s];
`, MinQ, MaxQ, MaxQ-MinQ+1, ArrayLiteral(flat(Pow5()), 2))
		},
	},
}

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
