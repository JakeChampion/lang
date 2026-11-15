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

type scalbnCase struct {
	bits uint64
	exp  int32
	want uint64
	nan  bool
}

func scalbnCases() []scalbnCase {
	var cases []scalbnCase
	seen := map[[2]uint64]bool{}
	add := func(bits uint64, exp int32) {
		key := [2]uint64{bits, uint64(uint32(exp))}
		if seen[key] {
			return
		}
		seen[key] = true
		want := math.Ldexp(math.Float64frombits(bits), int(exp))
		cases = append(cases, scalbnCase{bits, exp, math.Float64bits(want), math.IsNaN(want)})
	}
	magnitudes := []uint64{0, 1, 2, 3, 4, 0x000fffffffffffff, 0x0010000000000000,
		0x0010000000000001, 0x0020000000000000, 0x3fd0000000000000,
		0x3fe0000000000000, 0x3ff0000000000000, 0x3ff0000000000001,
		0x4000000000000000, 0x7fdfffffffffffff, 0x7fe0000000000000,
		0x7fefffffffffffff, 0x7ff0000000000000, 0x7ff8000000000001}
	exponents := []int32{-2147483648, -2147483647, -2147482625, -4096, -2100, -2099,
		-2048, -1076, -1075, -1074, -1073, -1024, -1023, -1022, -1021,
		-55, -54, -53, -2, -1, 0, 1, 2, 52, 53, 54, 1023, 1024, 1025,
		1026, 1074, 2098, 2100, 2147482623, 2147483646, 2147483647}
	for _, magnitude := range magnitudes {
		for _, sign := range []uint64{0, 1 << 63} {
			bits := magnitude | sign
			for _, exp := range exponents {
				add(bits, exp)
			}
			x := math.Float64frombits(bits)
			if x == 0 || math.IsNaN(x) || math.IsInf(x, 0) {
				continue
			}
			_, e := math.Frexp(x)
			for _, boundary := range []int{1024, -1021, -1074} {
				for delta := -2; delta <= 2; delta++ {
					add(bits, int32(boundary-e+delta))
				}
			}
		}
	}
	state := uint64(0xbb67ae8584caa73b)
	next := func() uint64 { state = state*6364136223846793005 + 1442695040888963407; return state }
	for i := 0; i < 256; i++ {
		bits := next()
		if bits&0x7ff0000000000000 == 0x7ff0000000000000 && bits&0xfffffffffffff != 0 {
			bits |= 0x8000000000000
		}
		add(bits, int32(next()>>32))
		add(bits, int32(next()%4401)-2200)
	}
	return cases
}

func scalbnProgram(cases []scalbnCase) string {
	var b strings.Builder
	b.WriteString(`import "std/f64";
import "std/i64";
@noinline function scale(x: f64, n: i32): f64 { return x.scalbn(n); }
function main(): i32 {
let inputs: i64[] = [`)
	for _, c := range cases {
		fmt.Fprintf(&b, "%d,", int64(c.bits))
	}
	b.WriteString("];\nlet exponents: i32[] = [")
	for _, c := range cases {
		fmt.Fprintf(&b, "%d,", c.exp)
	}
	b.WriteString(`];
for i in 0..inputs.len() { print(f64_bits(scale(f64_from_bits(inputs[i]), exponents[i])).to_string()); }
return 0;
}
`)
	return b.String()
}

func TestFloatScalbnBounds(t *testing.T) {
	cases := scalbnCases()
	source := scalbnProgram(cases)
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
			bits := uint64(n)
			if c.nan {
				if !math.IsNaN(math.Float64frombits(bits)) {
					t.Errorf("case %d x=%x n=%d: got %x, want NaN", i, c.bits, c.exp, bits)
				}
			} else if bits != c.want {
				t.Errorf("case %d x=%x n=%d: got %x, want %x", i, c.bits, c.exp, bits, c.want)
			}
		}
	}
	t.Run("go", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "scalbn.fern")
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
	t.Logf("%d scalbn controls with exact finite, infinite, and zero bits", len(cases))
}
