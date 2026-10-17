package e2ecompiler

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostArm64MultiplyConstants(t *testing.T) {
	cli := buildSelfHostCLI(t)
	coefficients := []int64{3, 5, 7, 9, 11, 17, 19, 33, 65, 129, 257, 513, 1025, 2049, 4095}
	values := []int64{0, 1, -1, 2, -2, 19, math.MinInt64, math.MaxInt64, 0x12345678abcdef}
	literal := func(v int64) string {
		if v == math.MinInt64 {
			return "(-9223372036854775807i64 - 1i64)"
		}
		return strconv.FormatInt(v, 10) + "i64"
	}
	var source strings.Builder
	for _, c := range coefficients {
		fmt.Fprintf(&source, "@noinline function right_%d(x: i64): i64 { return x * %di64; }\n", c, c)
		fmt.Fprintf(&source, "@noinline function left_%d(x: i64): i64 { return %di64 * x; }\n", c, c)
	}
	source.WriteString("function main(): i32 { let values: i64[] = [")
	for i, v := range values {
		if i > 0 {
			source.WriteString(", ")
		}
		source.WriteString(literal(v))
	}
	source.WriteString("];\n")
	for _, c := range coefficients {
		source.WriteString("{ let expected: i64[] = [")
		for i, v := range values {
			if i > 0 {
				source.WriteString(", ")
			}
			source.WriteString(literal(v * c))
		}
		fmt.Fprintf(&source, "]; let i: i32 = 0; while (i < values.len()) { if (right_%d(values[i]) != expected[i] || left_%d(values[i]) != expected[i]) { return 1; } i = i + 1; } }\n", c, c)
	}
	source.WriteString("return 0; }\n")
	if stderr, code := cli.exitOf(t, source.String(), "arm64-linux"); code != 0 {
		t.Fatalf("constant multiplication: exit %d, want 0\n%s", code, stderr)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int64{3, 5, 9, 17, 19, 33, 65, 129, 257, 513, 1025, 2049} {
		for _, side := range []string{"left", "right"} {
			body := tailCallBody(t, string(data), fmt.Sprintf("%s_%d", side, c))
			if regexp.MustCompile(`(?m)^\s+mul\s`).MatchString(body) {
				t.Errorf("%s_%d still materializes a constant for multiply:\n%s", side, c, body)
			}
			want := 1
			if c == 19 {
				want = 2
			}
			if got := len(regexp.MustCompile(`(?m)^\s+add\s`).FindAllString(body, -1)); got != want {
				t.Errorf("%s_%d: got %d adds, want %d\n%s", side, c, got, want, body)
			}
		}
	}
}

func TestSelfHostArm64MultiplyConstantWidths(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, kind := range []string{"i32", "u32", "u64"} {
		t.Run(kind, func(t *testing.T) {
			values := []uint64{0, 1, 2, 19, math.MaxInt32, 1 << 31, math.MaxUint32}
			if kind == "u64" {
				values = append(values, math.MaxInt64, 1<<63, math.MaxUint64)
			}
			literal := func(v uint64) string {
				if kind == "i32" {
					if int32(v) == math.MinInt32 {
						return "(-2147483647i32 - 1i32)"
					}
					return strconv.FormatInt(int64(int32(v)), 10) + kind
				}
				if kind == "u32" {
					v = uint64(uint32(v))
				}
				return strconv.FormatUint(v, 10) + kind
			}
			var source strings.Builder
			for _, c := range []uint64{3, 19, 2049} {
				fmt.Fprintf(&source, "@noinline function mul_%d(x: %s): %s { return x * %d%s; }\n", c, kind, kind, c, kind)
			}
			source.WriteString("function main(): i32 {\n")
			for _, c := range []uint64{3, 19, 2049} {
				for _, v := range values {
					fmt.Fprintf(&source, "if (mul_%d(%s) != %s) { return 1; }\n", c, literal(v), literal(v*c))
				}
			}
			source.WriteString("return 0; }\n")
			if stderr, code := cli.exitOf(t, source.String(), "arm64-linux"); code != 0 {
				t.Fatalf("%s multiplication: exit %d, want 0\n%s", kind, code, stderr)
			}
		})
	}
}
