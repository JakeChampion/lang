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

type hypotCase struct {
	width int
	args  []float64
	want  float64
}

func hypotSpecialCases() []hypotCase {
	var cases []hypotCase
	for _, width := range []int{64, 32} {
		add := func(args ...float64) {
			want := math.Hypot(args[0], args[1])
			if len(args) == 3 {
				want = math.Hypot(want, args[2])
			}
			if width == 32 {
				want = float64(float32(want))
			}
			cases = append(cases, hypotCase{width, args, want})
		}
		values := []float64{0, math.Copysign(0, -1), 3, -4, math.Inf(1), math.Inf(-1), math.NaN()}
		for _, x := range values {
			for _, y := range values {
				add(x, y)
				for _, z := range values {
					add(x, y, z)
				}
			}
		}
		max, smallest, normal := math.MaxFloat64, math.SmallestNonzeroFloat64, math.Float64frombits(0x0010000000000000)
		if width == 32 {
			max, smallest, normal = math.MaxFloat32, math.SmallestNonzeroFloat32, float64(math.Float32frombits(0x00800000))
		}
		add(max, max)
		add(max, 0)
		add(smallest, 0)
		add(normal, normal)
		add(max, 0, 0)
		add(smallest, 0, 0)
		add(normal, normal, normal)
		add(max/2, max/4, max/8)
	}
	return cases
}

func hypotSpecialProgram(cases []hypotCase) string {
	var b strings.Builder
	b.WriteString(`import "std/f64";
import "std/f32";
import "std/i64";
import "std/i32";
@noinline
function h64(x: f64, y: f64): f64 { return x.hypot(y); }
@noinline
function h364(x: f64, y: f64, z: f64): f64 { return x.hypot3(y, z); }
@noinline
function h32(x: f32, y: f32): f32 { return x.hypot(y); }
@noinline
function h332(x: f32, y: f32, z: f32): f32 { return x.hypot3(y, z); }
function main(): i32 {
`)
	for _, c := range cases {
		var args []string
		for _, x := range c.args {
			if c.width == 32 {
				args = append(args, fmt.Sprintf("f32_from_bits(%d)", int32(math.Float32bits(float32(x)))))
			} else {
				args = append(args, fmt.Sprintf("f64_from_bits(%d)", int64(math.Float64bits(x))))
			}
		}
		name := fmt.Sprintf("h%d", c.width)
		if len(c.args) == 3 {
			name = fmt.Sprintf("h3%d", c.width)
		}
		fmt.Fprintf(&b, "print(f%d_bits(%s(%s)).to_string());\n", c.width, name, strings.Join(args, ", "))
	}
	b.WriteString("return 0;\n}\n")
	return b.String()
}

func TestFloatHypotSpecialValues(t *testing.T) {
	cases := hypotSpecialCases()
	if len(cases) != 800 {
		t.Fatalf("case count = %d, want 800", len(cases))
	}
	source := hypotSpecialProgram(cases)
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
			got, gotBits, wantBits := math.Float64frombits(uint64(bits)), uint64(bits), math.Float64bits(c.want)
			if c.width == 32 {
				gotBits, wantBits = uint64(uint32(bits)), uint64(math.Float32bits(float32(c.want)))
				got = float64(math.Float32frombits(uint32(bits)))
			}
			switch {
			case math.IsNaN(c.want):
				if !math.IsNaN(got) {
					t.Errorf("case %d f%d %v: got %v, want NaN", i, c.width, c.args, got)
				}
			case math.IsInf(c.want, 1), c.want == 0:
				if gotBits != wantBits {
					t.Errorf("case %d f%d %v: got bits %x, want %x", i, c.width, c.args, gotBits, wantBits)
				}
			default:
				distance := gotBits - wantBits
				if gotBits < wantBits {
					distance = wantBits - gotBits
				}
				if math.IsNaN(got) || math.IsInf(got, 0) || distance > uint64(maxULP) {
					t.Errorf("case %d f%d %v: got %v, want %v within %d ULP", i, c.width, c.args, got, c.want, maxULP)
				}
			}
		}
	}
	t.Run("go", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "hypot.fern")
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
	t.Logf("%d norm controls; special values exact, finite values within the existing %d ULP bound", len(cases), maxULP)
}
