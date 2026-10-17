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
	coefficients := []int64{2, 3, 4, 5, 7, 8, 9, 11, 16, 17, 19, 32, 33, 64, 65, 128, 129, 256, 257, 512, 513, 1024, 1025, 2048, 2049, 4095}
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
	_, runner := arm64Tooling(t)
	if output, err := runArm64Bin(runner, cli.arm64Binary(t, src)).CombinedOutput(); err != nil {
		t.Fatalf("native encoder constant multiplication: %v\n%s", err, output)
	}
	data, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int64{2, 3, 4, 5, 8, 9, 16, 17, 32, 33, 64, 65, 128, 129, 256, 257, 512, 513, 1024, 1025, 2048, 2049} {
		for _, side := range []string{"left", "right"} {
			body := tailCallBody(t, string(data), fmt.Sprintf("%s_%d", side, c))
			if regexp.MustCompile(`(?m)^\s+mul\s`).MatchString(body) {
				t.Errorf("%s_%d still materializes a constant for multiply:\n%s", side, c, body)
			}
			op := "add"
			if c&(c-1) == 0 {
				op = "lsl"
			}
			if got := len(regexp.MustCompile(`(?m)^\s+`+op+`\s`).FindAllString(body, -1)); got != 1 {
				t.Errorf("%s_%d: got %d %s instructions, want 1\n%s", side, c, got, op, body)
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
			for _, c := range []uint64{2, 3, 19, 2048, 2049} {
				fmt.Fprintf(&source, "@noinline function mul_%d(x: %s): %s { return x * %d%s; }\n", c, kind, kind, c, kind)
			}
			source.WriteString("function main(): i32 {\n")
			for _, c := range []uint64{2, 3, 19, 2048, 2049} {
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

func TestSelfHostArm64MultiplyConstantReuse(t *testing.T) {
	const source = `@noinline function touch(x: i64): i64 { return x + 1i64; }
@noinline function adjacent(a: i64, b: i64): i64 {
    let x = a * 19i64;
    let y = 19i64 * b;
    return x + y;
}
@noinline function separated(a: i64, b: i64): i64 {
    let x = touch(a * 19i64);
    return x + b * 19i64;
}
@noinline function branched(a: i64, b: i64, flag: boolean): i64 {
    let x = a * 19i64;
    if (flag) { return touch(x) + b * 19i64; }
    return x + 19i64 * b;
}
function main(): i32 {
    if (adjacent(3i64, 5i64) != 152i64) { return 1; }
    if (separated(3i64, 5i64) != 153i64) { return 2; }
    if (branched(3i64, 5i64, true) != 153i64) { return 3; }
    if (branched(3i64, 5i64, false) != 152i64) { return 4; }
    return 0;
}`
	cli := buildSelfHostCLI(t)
	if stderr, code := cli.exitOf(t, source, "arm64-linux"); code != 0 {
		t.Fatalf("constant reuse: exit %d, want 0\n%s", code, stderr)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cli.emit(t, src, "arm64-linux"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"adjacent": 1, "separated": 2} {
		body := tailCallBody(t, string(data), name)
		if got := len(regexp.MustCompile(`(?m)^\s+mov x5, #19$`).FindAllString(body, -1)); got != want {
			t.Errorf("%s materializes 19 %d times, want %d\n%s", name, got, want, body)
		}
		if got := len(regexp.MustCompile(`(?m)^\s+mul\s`).FindAllString(body, -1)); got != 2 {
			t.Errorf("%s has %d multiplies, want 2\n%s", name, got, body)
		}
	}
}
