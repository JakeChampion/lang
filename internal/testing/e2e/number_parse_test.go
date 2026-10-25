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

// Compare decimal inputs on both sides of exact binary32 midpoints with Go.
// Include zero, the normal boundary, powers of two, and the overflow midpoint.
func TestF32ParserCorrectlyRounded(t *testing.T) {
	lower := []uint32{0, 1, 2, 0x007ffffe, 0x007fffff, 0x00800000, 0x00800001, 0x3f7fffff, 0x3f800000, 0x3f800001, 0x7f7ffffe, 0x7f7fffff}
	random := rand.New(rand.NewSource(12713))
	for i := 0; i < 20; i++ {
		lower = append(lower, uint32(random.Int31())%0x7f800000)
	}
	delta := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(200), nil))
	var source strings.Builder
	source.WriteString("import \"std/f32\";\nimport \"std/number_parse\";\n")
	source.WriteString("struct Case { text: string, bits: i32, ranged: boolean }\nfunction main(): i32 {\nlet cases: Case[] = [\n")
	for _, bits := range lower {
		a := new(big.Rat).SetFloat64(float64(math.Float32frombits(bits)))
		var b *big.Rat
		if bits+1 == 0x7f800000 {
			b = new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 128))
		} else {
			b = new(big.Rat).SetFloat64(float64(math.Float32frombits(bits + 1)))
		}
		mid := new(big.Rat).Quo(new(big.Rat).Add(a, b), big.NewRat(2, 1))
		for _, adjustment := range []int64{-1, 0, 1} {
			exact := new(big.Rat).Add(mid, new(big.Rat).Mul(delta, big.NewRat(adjustment, 1)))
			for _, sign := range []string{"", "-"} {
				text := sign + exact.FloatString(220)
				value, err := strconv.ParseFloat(text, 32)
				fmt.Fprintf(&source, "Case { text: %q, bits: %d, ranged: %v },\n", text, int32(math.Float32bits(float32(value))), err != nil)
			}
		}
	}
	source.WriteString("];\nlet index: i32 = 0;\nfor c in cases {\nmatch (f32.parse_f32(c.text)) {\n")
	source.WriteString("Ok(n) => { if (c.ranged || f32_bits(n) != c.bits) { print(index); return 1; } },\n")
	source.WriteString("Err(e) => { if (!c.ranged) { print(index); return 2; } match (e) { number_parse.OutOfRange => {}, _ => { return 3; } } }\n")
	source.WriteString("}\nindex = index + 1;\n}\nreturn 0;\n}\n")
	program := source.String()
	t.Run("interp", func(t *testing.T) {
		if got := runInterpExit(t, program); got != 0 {
			t.Fatalf("exit = %d", got)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if out, got := compileAndRunX86_64(t, program); got != 0 {
			t.Fatalf("exit = %d\n%s", got, out)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if out, got := compileAndRunArm64(t, program); got != 0 {
			t.Fatalf("exit = %d\n%s", got, out)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if got := compileAndRunWasmbinMain(t, program); got != 0 {
			t.Fatalf("exit = %d", got)
		}
	})
}
