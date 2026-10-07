package e2ecompiler

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Values alone do not distinguish a kernel from std/ndarray's scalar walk.
// Check emitted code at the actual call site as well as immutable aliases,
// shape and element order. A transpose deliberately produces different order.
const ndarrayScaleKernelSrc = `import "std/ndarray";

@noinline function packed_scale(xs: f64[]): ndarray.NdArray[f64] {
  let a: ndarray.NdArray[f64] = ndarray.from_flat(xs, [2, 3]);
  return a.map((x: f64): f64 => x * 2.0);
}

@noinline function half(x: f64): f64 { return x * 0.5; }
@noinline function named_scale(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map(half);
}

@noinline function strided_scale(xs: f64[]): ndarray.NdArray[f64] {
  let a: ndarray.NdArray[f64] = ndarray.from_flat(xs, [2, 3]).transpose();
  return a.map((x: f64): f64 => x * 2.0);
}

@noinline function unknown_scale(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}

@noinline function add_map(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map((x: f64): f64 => x + 2.0);
}

@noinline function packed_dynamic(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [xs.len()]).map((x: f64): f64 => x * 2.0);
}

@noinline function zero_scale(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [xs.len()]).map((x: f64): f64 => x * 0.0);
}
@noinline function reversed_scale(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [xs.len()]).map((x: f64): f64 => 2.0 * x);
}
@noinline function scalar_product(x: f64, y: f64): f64 { return x * y; }

function main(): i32 {
  let xs: f64[] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0];
  let a: ndarray.NdArray[f64] = packed_scale(xs);
  let b: ndarray.NdArray[f64] = strided_scale(xs);
  let c: ndarray.NdArray[f64] = named_scale(xs);
  let d: ndarray.NdArray[f64] = unknown_scale(ndarray.from_flat(xs, [2, 3]).transpose());
  let e: ndarray.NdArray[f64] = add_map(xs);
  let i: i32 = 0;
  while (i < 6) {
    if (a.get([i / 3, i % 3]) != xs[i] * 2.0) { return 10 + i; }
    if (b.get([i % 3, i / 3]) != xs[i] * 2.0) { return 20 + i; }
    if (c.get([i / 3, i % 3]) != xs[i] * 0.5) { return 30 + i; }
    if (d.get([i % 3, i / 3]) != xs[i] * 2.0) { return 40 + i; }
    if (e.get([i / 3, i % 3]) != xs[i] + 2.0) { return 50 + i; }
    if (xs[i] != (i + 1) as f64) { return 60 + i; }
    i = i + 1;
  }
  if (a.shape()[0] != 2 || a.shape()[1] != 3 || !a.is_packed()) { return 70; }
  if (b.shape()[0] != 3 || b.shape()[1] != 2 || !b.is_packed()) { return 71; }
  // Empty input, a singleton and both vector and scalar tails. Retain the
  // original input and compare every element, not only the endpoints.
  let n: i32 = 0;
  while (n < 10) {
    let input: f64[] = [];
    let j: i32 = 0;
    while (j < n) { input = input.append((j - 3) as f64); j = j + 1; }
    let mapped: ndarray.NdArray[f64] = packed_dynamic(input);
    if (mapped.len() != n || mapped.shape()[0] != n || !mapped.is_packed()) { return 80; }
    j = 0;
    while (j < n) {
      if (mapped.get([j]) != input[j] * 2.0 || input[j] != (j - 3) as f64) { return 81; }
      j = j + 1;
    }
    n = n + 1;
  }
  let bits: i64[] = [0i64 - 9223372036854775807i64 - 1i64,
    9218868437227405312i64, 0i64 - 4503599627370496i64,
    9221120237041090625i64, 9221120237041090626i64, 9218868437227405313i64];
  let special: f64[] = [];
  for bit in bits { special = special.append(f64_from_bits(bit)); }
  let twice = packed_dynamic(special).to_flat();
  let zero = zero_scale(special).to_flat();
  let reversed = reversed_scale(special).to_flat();
  i = 0;
  while (i < special.len()) {
    if (f64_bits(twice[i]) != f64_bits(scalar_product(special[i], 2.0))) { return 82; }
    if (f64_bits(zero[i]) != f64_bits(scalar_product(special[i], 0.0))) { return 83; }
    if (f64_bits(reversed[i]) != f64_bits(scalar_product(2.0, special[i]))) { return 84; }
    if (f64_bits(special[i]) != bits[i]) { return 85; }
    i = i + 1;
  }
  if (__rc_underflow_count() != 0) { return 86; }
  return 0;
}
`

func TestSelfHostNdarrayScaleKernelX86_64(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, dir := compileSourceModload(t, runner, driver, ndarrayScaleKernelSrc)
	bin := buildBin(t, gcc, dir, "ndarray-scale", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("ndarray scale: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name   string
		kernel bool
	}{
		{"packed_scale", true},
		{"packed_dynamic", true},
		{"zero_scale", true},
		{"reversed_scale", false},
		// The noinline named function is outside this slice's literal-body
		// proof and remains a scalar map.
		{"named_scale", false},
		{"strided_scale", false},
		{"unknown_scale", false},
		{"add_map", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := emittedBody(t, string(asm), "__fn_"+tc.name)
			mapped := strings.Contains(body, "call __fn___smm_ndarray__NdArray_map__")
			vector := strings.Contains(body, "vbroadcastsd")
			if mapped == tc.kernel || vector != tc.kernel {
				t.Fatalf("kernel=%t, map call=%t, vector splat=%t\n%s", tc.kernel, mapped, vector, body)
			}
		})
	}
	cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
	cmd.Env = append(os.Environ(), "FERN_NO_SCALE_KERNEL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	disabled, err := cmd.Output()
	if err != nil {
		t.Fatalf("compile disabled scale kernel: %v\n%s", err, stderr.String())
	}
	for _, name := range []string{"packed_scale", "packed_dynamic", "zero_scale"} {
		body := emittedBody(t, string(disabled), "__fn_"+name)
		if !strings.Contains(body, "call __fn___smm_ndarray__NdArray_map__") || strings.Contains(body, "vbroadcastsd") {
			t.Fatalf("disabled kernel still rewrote %s:\n%s", name, body)
		}
	}
}

func TestSelfHostNdarrayScaleKernelLeakCensus(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, ndarrayScaleKernelSrc,
						[]string{"FERN_LEAKCHECK=1", "FERN_NO_SCALE_KERNEL=" + disabled})
					out, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil {
						t.Fatalf("scale kernel census: %v\n%s", err, out)
					}
					line := leakSummaryLine(string(out))
					var allocs, frees, live int64
					if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil {
						t.Fatalf("missing census: %v\n%s", err, out)
					}
					if allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("unbalanced scale ownership: %s", line)
					}
				})
			}
		})
	}
}

func TestSelfHostNdarrayScaleKernelBenchmark(t *testing.T) {
	src := filepath.Join(repoRootFromTest(t), "examples", "array_pipeline", "ndarray_scale.fern")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				bin := e2eharness.CompileSelfHostFile(t, target, src, []string{"FERN_NO_SCALE_KERNEL=" + disabled})
				out, err := runScaleTarget(t, target, bin, "17", "2").CombinedOutput()
				if err != nil {
					t.Fatalf("scale benchmark disabled=%s: %v\n%s", disabled, err, out)
				}
				var report struct {
					N        int   `json:"n"`
					Rounds   int   `json:"rounds"`
					Checksum int64 `json:"checksum"`
				}
				if err := json.Unmarshal(bytes.TrimSpace(out), &report); err != nil {
					t.Fatalf("benchmark report: %v\n%s", err, out)
				}
				// Each round sums 2*i for i in [0, 17).
				if report.N != 17 || report.Rounds != 2 || report.Checksum != 544 {
					t.Fatalf("incorrect benchmark result: %+v", report)
				}
			}
		})
	}
}

func TestSelfHostNdarrayScaleKernelValues(t *testing.T) {
	for _, target := range []string{e2eharness.TargetArm64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			if target == e2eharness.TargetArm64Linux {
				if _, code := e2eharness.CompileAndRunArm64(t, ndarrayScaleKernelSrc); code != 0 {
					t.Fatalf("ndarray scale exited %d", code)
				}
				return
			}
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(ndarrayScaleKernelSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			core := e2eharness.CompileSelfHostFile(t, target, src, nil)
			cmd := e2eharness.RunWasmCore(t, core)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ndarray scale: %v\n%s", err, out)
			}
		})
	}
}
