package e2e

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type midpointCase struct {
	width      int
	a, b, want uint64
	nan        bool
}

func midpointCases() []midpointCase {
	var cases []midpointCase
	for _, width := range []int{64, 32} {
		magnitudes := []uint64{0, 1, 2, 3, 0x000fffffffffffff, 0x0010000000000000,
			0x0010000000000001, 0x0020000000000000, 0x3ff0000000000000,
			0x3ff0000000000001, 0x4000000000000000, 0x7fdfffffffffffff,
			0x7fe0000000000000, 0x7fefffffffffffff, 0x7ff0000000000000, 0x7ff8000000000001}
		sign := uint64(1) << 63
		if width == 32 {
			magnitudes = []uint64{0, 1, 2, 3, 0x007fffff, 0x00800000, 0x00800001,
				0x01000000, 0x3f800000, 0x3f800001, 0x40000000, 0x7effffff,
				0x7f000000, 0x7f7fffff, 0x7f800000, 0x7fc00001}
			sign = 1 << 31
		}
		add := func(a, b uint64) {
			x, y := math.Float64frombits(a), math.Float64frombits(b)
			if width == 32 {
				x, y = float64(math.Float32frombits(uint32(a))), float64(math.Float32frombits(uint32(b)))
			}
			want := x*0.5 + y*0.5
			if !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0) && (x != 0 || y != 0) {
				r := new(big.Rat).Add(new(big.Rat).SetFloat64(x), new(big.Rat).SetFloat64(y))
				r.Quo(r, big.NewRat(2, 1))
				if width == 32 {
					f, _ := r.Float32()
					want = float64(f)
				} else {
					want, _ = r.Float64()
				}
			}
			bits := math.Float64bits(want)
			if width == 32 {
				bits = uint64(math.Float32bits(float32(want)))
			}
			cases = append(cases, midpointCase{width, a, b, bits, math.IsNaN(want)})
		}
		var values []uint64
		for _, magnitude := range magnitudes {
			values = append(values, magnitude, magnitude|sign)
		}
		for _, a := range values {
			for _, b := range values {
				add(a, b)
			}
		}
		state := uint64(0x6a09e667f3bcc909)
		next := func() uint64 {
			state = state*6364136223846793005 + 1442695040888963407
			bits := state
			if width == 32 {
				bits = uint64(uint32(state >> 32))
				if bits&0x7f800000 == 0x7f800000 && bits&0x007fffff != 0 {
					bits |= 0x00400000
				}
			} else if bits&0x7ff0000000000000 == 0x7ff0000000000000 && bits&0x000fffffffffffff != 0 {
				bits |= 0x0008000000000000
			}
			return bits
		}
		for i := 0; i < 128; i++ {
			add(next(), next())
		}
	}
	return cases
}

func midpointProgram(cases []midpointCase) string {
	var b strings.Builder
	b.WriteString(`import "std/f32";
import "std/f64";
import "std/i32";
import "std/i64";
@noinline function mid64(a: f64, b: f64): f64 { return a.midpoint(b); }
@noinline function mid32(a: f32, b: f32): f32 { return a.midpoint(b); }
function main(): i32 {
`)
	for _, width := range []int{64, 32} {
		for _, field := range []string{"a", "b"} {
			fmt.Fprintf(&b, "let %s%d: i%d[] = [", field, width, width)
			for _, c := range cases {
				if c.width != width {
					continue
				}
				bits := c.a
				if field == "b" {
					bits = c.b
				}
				if width == 32 {
					fmt.Fprintf(&b, "%d,", int32(bits))
				} else {
					fmt.Fprintf(&b, "%d,", int64(bits))
				}
			}
			b.WriteString("];\n")
		}
		fmt.Fprintf(&b, "for i in 0..a%d.len() { print(f%d_bits(mid%d(f%d_from_bits(a%d[i]), f%d_from_bits(b%d[i]))).to_string()); }\n",
			width, width, width, width, width, width, width)
	}
	b.WriteString("return 0;\n}\n")
	return b.String()
}

func TestFloatMidpointBounds(t *testing.T) {
	cases := midpointCases()
	if len(cases) != 2304 {
		t.Fatalf("case count = %d, want 2304", len(cases))
	}
	source := midpointProgram(cases)
	check := func(t *testing.T, output string) {
		t.Helper()
		lines := strings.Fields(output)
		if len(lines) != len(cases) {
			t.Fatalf("got %d results, want %d", len(lines), len(cases))
		}
		for i, c := range cases {
			n, err := strconv.ParseInt(lines[i], 10, 64)
			if err != nil {
				t.Fatalf("result %d: %v", i, err)
			}
			bits, value := uint64(n), math.Float64frombits(uint64(n))
			if c.width == 32 {
				bits = uint64(uint32(n))
				value = float64(math.Float32frombits(uint32(n)))
			}
			if c.nan {
				if !math.IsNaN(value) {
					t.Errorf("case %d f%d a=%x b=%x: got %x, want NaN", i, c.width, c.a, c.b, bits)
				}
			} else if bits != c.want {
				t.Errorf("case %d f%d a=%x b=%x: got %x, want %x", i, c.width, c.a, c.b, bits, c.want)
			}
		}
	}
	t.Run("go", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "midpoint.fern")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out, stderr := runLangInterp(t, buildLangBinForInterp(t), path)
		if code != 0 {
			t.Fatalf("Go exit %d: %s", code, stderr)
		}
		check(t, out)
	})
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, source)
		if code != 0 {
			t.Fatalf("x86-64 exit %d: %s", code, out)
		}
		check(t, out)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64(t, source)
		if code != 0 {
			t.Fatalf("ARM64 exit %d: %s", code, out)
		}
		check(t, out)
	})
	t.Run("wasm", func(t *testing.T) { check(t, compileAndRunWasmCapture(t, source)) })
	t.Logf("%d exact finite midpoint and IEEE special-value controls", len(cases))
}
