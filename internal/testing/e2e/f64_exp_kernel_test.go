package e2e

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func expKernelCases() (cases []f64Case, inputs []int64, exact int) {
	seen := map[uint64]bool{}
	add := func(x float64) {
		bits := math.Float64bits(x)
		if seen[bits] {
			return
		}
		seen[bits] = true
		var want float64
		switch {
		case math.IsNaN(x):
			want = math.NaN()
		case math.IsInf(x, 1):
			want = math.Inf(1)
		case math.IsInf(x, -1):
			want = 0
		default:
			want, _ = bigExp(newRef().SetFloat64(x)).Float64()
		}
		inputs = append(inputs, int64(bits))
		cases = append(cases, f64Case{fmt.Sprintf("fexp(f64_from_bits(%d))", int64(bits)), want})
	}
	for _, x := range []float64{0, math.Copysign(0, -1), 1, -1, 0.5, -0.5, 2, -2, 10, -10, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		add(x)
	}
	exact = len(cases)
	for _, edge := range []float64{-745.1332191019411, -744.4400719213812, -709.0, -708.3964185322641, -512, -1e-16, 1e-16, 512, 709.782712893384} {
		add(edge)
		lo, hi := edge, edge
		for range 8 {
			lo = math.Nextafter(lo, math.Inf(-1))
			hi = math.Nextafter(hi, math.Inf(1))
			add(lo)
			add(hi)
		}
	}
	for k := -137000; k < 131000; k += 997 {
		x := (float64(k) + 0.5) * math.Ln2 / 128
		add(x)
		add(math.Nextafter(x, math.Inf(-1)))
		add(math.Nextafter(x, math.Inf(1)))
	}
	seed := uint64(0x4816b995bcd1)
	for range 2048 {
		seed = seed*6364136223846793005 + 1442695040888963407
		add(-746 + 1456*float64(seed>>11)*0x1p-53)
	}
	return
}

func expKernelProgram(inputs []int64) string {
	var b strings.Builder
	b.WriteString("import \"std/f64\";\nimport \"std/i64\";\n@noinline\nfunction fexp(x: f64): f64 { return x.exp(); }\nfunction main(): i32 {\nlet inputs: i64[] = [\n")
	for _, bits := range inputs {
		fmt.Fprintf(&b, "%d,\n", bits)
	}
	b.WriteString("];\nfor bits in inputs { print(f64_bits(fexp(f64_from_bits(bits))).to_string()); }\nreturn 0;\n}\n")
	return b.String()
}

func TestF64ExpSharedKernel(t *testing.T) {
	cases, inputs, exact := expKernelCases()
	source := expKernelProgram(inputs)
	check := func(t *testing.T, label, output string) {
		t.Helper()
		checkF64Output(t, label, output, cases, 1)
		lines := strings.Fields(output)
		checkF64Output(t, label+" exact", strings.Join(lines[:exact], "\n"), cases[:exact], 0)
	}
	t.Run("interp", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "exp.fern")
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out, errOut := runLangInterp(t, buildLangBinForInterp(t), path)
		if code != 0 {
			t.Fatalf("interp exit %d\n%s", code, errOut)
		}
		check(t, "interp", out)
	})
	t.Run("x86-64", func(t *testing.T) {
		out, code := compileAndRunX86_64(t, source)
		if code != 0 {
			t.Fatalf("x86-64 exit %d\n%s", code, out)
		}
		check(t, "x86-64", out)
	})
	t.Run("arm64", func(t *testing.T) {
		out, code := compileAndRunArm64(t, source)
		if code != 0 {
			t.Fatalf("arm64 exit %d\n%s", code, out)
		}
		check(t, "arm64", out)
	})
	t.Run("wasm", func(t *testing.T) { check(t, "wasm", compileAndRunWasmCapture(t, source)) })
	t.Logf("%d exp inputs against the 256-bit reference, with %d exact controls", len(cases), exact)
}

const expNoHeapSource = `import "std/f64";
@noinline
function evaluate(x: f64): f64 { return x.exp(); }
@noinline
function allocate(n: i32): i32[] { return __alloc_i32(n); }
function main(): i32 {
 let before = __heap_alloc_count();
 let total = 0.0;
 for i in 0..257 { total += evaluate((i as f64) / 32.0 - 4.0); }
 if (__heap_alloc_count() != before || total <= 0.0) { return 1; }
 let control_before = __heap_alloc_count();
 let control = allocate(args().len() + 13);
 if (control.len() < 13 || __heap_alloc_count() <= control_before) { return 2; }
 return 0;
}
`

func TestF64ExpNoHeapAllocation(t *testing.T) {
	t.Run("x86-64", func(t *testing.T) {
		if out, code := compileAndRunX86_64(t, expNoHeapSource); code != 0 {
			t.Fatalf("x86-64 exit %d\n%s", code, out)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if out, code := compileAndRunArm64(t, expNoHeapSource); code != 0 {
			t.Fatalf("arm64 exit %d\n%s", code, out)
		}
	})
	t.Run("wasm", func(t *testing.T) { compileAndRunWasmCapture(t, expNoHeapSource) })
}
