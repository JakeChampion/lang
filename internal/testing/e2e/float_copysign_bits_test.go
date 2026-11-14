package e2e

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type copySignCase struct {
	width   int
	x, sign uint64
	want    uint64
}

func copySignCases() []copySignCase {
	var cases []copySignCase
	for _, width := range []int{64, 32} {
		magnitudes := []uint64{0, 1, 0x000fffffffffffff, 0x0010000000000000,
			0x3ff0000000000000, 0x400921fb54442d18, 0x7fefffffffffffff,
			0x7ff0000000000000, 0x7ff8000000000001, 0x7fffffffffffffff}
		signBit := uint64(1) << 63
		if width == 32 {
			magnitudes = []uint64{0, 1, 0x007fffff, 0x00800000, 0x3f800000,
				0x40490fdb, 0x7f7fffff, 0x7f800000, 0x7fc00001, 0x7fffffff}
			signBit = 1 << 31
		}
		add := func(x, sign uint64) {
			want := math.Float64bits(math.Copysign(math.Float64frombits(x), math.Float64frombits(sign)))
			if width == 32 {
				xf, sf := math.Float32frombits(uint32(x)), math.Float32frombits(uint32(sign))
				want = uint64(math.Float32bits(float32(math.Copysign(float64(xf), float64(sf)))))
			}
			cases = append(cases, copySignCase{width, x, sign, want})
		}
		var values []uint64
		for _, magnitude := range magnitudes {
			values = append(values, magnitude, magnitude|signBit)
		}
		for _, x := range values {
			for _, sign := range values {
				add(x, sign)
			}
		}
		state := uint64(0x36a9d37e42b5c601)
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

func copySignProgram(cases []copySignCase) string {
	var b strings.Builder
	b.WriteString(`import "std/f32";
import "std/f64";
import "std/i32";
import "std/i64";
@noinline function copy64(x: f64, sign: f64): f64 { return x.copysign(sign); }
@noinline function copy32(x: f32, sign: f32): f32 { return x.copysign(sign); }
function main(): i32 {
`)
	for _, width := range []int{64, 32} {
		for _, field := range []string{"x", "sign"} {
			fmt.Fprintf(&b, "let %s%d: i%d[] = [", field, width, width)
			for _, c := range cases {
				if c.width == width {
					bits := c.x
					if field == "sign" {
						bits = c.sign
					}
					if width == 32 {
						fmt.Fprintf(&b, "%d,", int32(bits))
					} else {
						fmt.Fprintf(&b, "%d,", int64(bits))
					}
				}
			}
			b.WriteString("];\n")
		}
		fmt.Fprintf(&b, "for i in 0..x%d.len() { print(f%d_bits(copy%d(f%d_from_bits(x%d[i]), f%d_from_bits(sign%d[i]))).to_string()); }\n",
			width, width, width, width, width, width, width)
	}
	b.WriteString("return 0;\n}\n")
	return b.String()
}

func TestFloatCopySignBits(t *testing.T) {
	cases := copySignCases()
	if len(cases) != 1056 {
		t.Fatalf("case count = %d, want 1056", len(cases))
	}
	source := copySignProgram(cases)
	check := func(t *testing.T, output string) {
		t.Helper()
		lines := strings.Fields(output)
		if len(lines) != len(cases) {
			t.Fatalf("got %d results, want %d", len(lines), len(cases))
		}
		for i, c := range cases {
			bits, err := strconv.ParseInt(lines[i], 10, 64)
			if err != nil {
				t.Fatalf("result %d: %v", i, err)
			}
			got := uint64(bits)
			if c.width == 32 {
				got = uint64(uint32(bits))
			}
			if got != c.want {
				t.Errorf("case %d f%d x=%x sign=%x: got bits %x, want %x", i, c.width, c.x, c.sign, got, c.want)
			}
		}
	}
	t.Run("go", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "copysign.fern")
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
	t.Logf("%d exact-bit copysign controls", len(cases))
}
