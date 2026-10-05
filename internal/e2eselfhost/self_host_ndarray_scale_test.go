package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
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
			vector := strings.Contains(body, "unpcklpd")
			if mapped == tc.kernel || vector != tc.kernel {
				t.Fatalf("kernel=%t, map call=%t, vector splat=%t\n%s", tc.kernel, mapped, vector, body)
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
