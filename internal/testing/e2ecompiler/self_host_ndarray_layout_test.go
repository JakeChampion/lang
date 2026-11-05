package e2ecompiler

import (
	"strings"
	"testing"
)

const ndarrayLayoutSrc = `import "std/ndarray";

@noinline function grid(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]);
}
@noinline function helper_scale(xs: f64[]): ndarray.NdArray[f64] {
  return grid(xs).map((x: f64): f64 => x * 2.0);
}
@noinline function parameter_scale(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@noinline function mixed_scale(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@noinline function reversed_calls(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@noinline function recursive(a: ndarray.NdArray[f64], n: i32): ndarray.NdArray[f64] {
  if (n > 0) { return recursive(a, n - 1); }
  return a;
}
@noinline function recursive_scale(xs: f64[]): ndarray.NdArray[f64] {
  return recursive(grid(xs), 3).map((x: f64): f64 => x * 2.0);
}
@noinline function mixed_recursive(a: ndarray.NdArray[f64], n: i32): ndarray.NdArray[f64] {
  if (n > 0) { return mixed_recursive(a.transpose(), n - 1); }
  return a;
}
@noinline function mixed_recursive_scale(xs: f64[]): ndarray.NdArray[f64] {
  return mixed_recursive(grid(xs), 1).map((x: f64): f64 => x * 2.0);
}
@noinline function no_base(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] { return no_base(a); }
@noinline function maybe_cycle(a: ndarray.NdArray[f64], n: i32): ndarray.NdArray[f64] {
  if (n > 1000) { return no_base(a); }
  return a;
}
@noinline function unseeded_scale(xs: f64[], n: i32): ndarray.NdArray[f64] {
  return maybe_cycle(grid(xs), n).map((x: f64): f64 => x * 2.0);
}
@noinline function second(a: ndarray.NdArray[f64], b: ndarray.NdArray[f64]): ndarray.NdArray[f64] { return b; }
@noinline function second_scale(xs: f64[]): ndarray.NdArray[f64] {
  let a: ndarray.NdArray[f64] = grid(xs);
  return second(a, a.transpose()).map((x: f64): f64 => x * 2.0);
}
@noinline function repacked_scale(xs: f64[]): ndarray.NdArray[f64] {
  return grid(xs).transpose().packed().map((x: f64): f64 => x * 2.0);
}
@noinline function indirect_scale(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@export("fern:array/layout", "scale")
function exported_scale(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@noinline function apply(f: (ndarray.NdArray[f64]) => ndarray.NdArray[f64], a: ndarray.NdArray[f64]): ndarray.NdArray[f64] { return f(a); }

function check(a: ndarray.NdArray[f64], xs: f64[], transposed: boolean): boolean {
  let i: i32 = 0;
  while (i < xs.len()) {
    let row: i32 = i / 3;
    let col: i32 = i % 3;
    if (transposed) { row = i % 3; col = i / 3; }
    if (a.get_unchecked([row, col]) != xs[i] * 2.0) { return false; }
    i = i + 1;
  }
  return true;
}
function main(): i32 {
  let xs: f64[] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0];
  let a: ndarray.NdArray[f64] = grid(xs);
  if (!check(helper_scale(xs), xs, false)) { return 1; }
  if (!check(parameter_scale(a), xs, false)) { return 2; }
  if (!check(mixed_scale(a), xs, false)) { return 3; }
  if (!check(mixed_scale(a.transpose()), xs, true)) { return 4; }
  if (!check(reversed_calls(a.transpose()), xs, true)) { return 5; }
  if (!check(reversed_calls(a), xs, false)) { return 6; }
  if (!check(recursive_scale(xs), xs, false)) { return 7; }
  if (!check(second_scale(xs), xs, true)) { return 8; }
  if (!check(repacked_scale(xs), xs, true)) { return 9; }
  if (!check(apply(indirect_scale, a.transpose()), xs, true)) { return 10; }
  // A visible packed call cannot erase the indirect caller's unknown input.
  if (!check(indirect_scale(a), xs, false)) { return 11; }
  if (a.get_unchecked([1, 2]) != 6.0 || xs[5] != 6.0) { return 12; }
  // Its only visible caller is packed. An external caller is unrestricted.
  if (!check(exported_scale(a), xs, false)) { return 13; }
  if (!check(mixed_recursive_scale(xs), xs, true)) { return 14; }
  // The no-base arm is not executed. Analyzing it must still terminate.
  if (!check(unseeded_scale(xs, args().len()), xs, false)) { return 15; }
  return 0;
}
`

type layoutCase struct {
	name   string
	kernel bool
}

func TestSelfHostNdarrayLayoutPropagation(t *testing.T) {
	checkLayoutKernels(t, []layoutCase{
		{"helper_scale", true},
		{"parameter_scale", true},
		{"recursive_scale", true},
		{"repacked_scale", true},
		{"mixed_scale", false},
		{"reversed_calls", false},
		{"second_scale", false},
		{"indirect_scale", false},
		{"exported_scale", false},
		{"mixed_recursive_scale", false},
		// semreach proves that no_base cannot return, so maybe_cycle returns a packed array.
		{"unseeded_scale", true},
	})
}

// Without semreach the layout pass sees the no-base cycle itself. Its
// unseen return must become unknown, not a packed-layout proof.
func TestSelfHostNdarrayLayoutUnseenReturn(t *testing.T) {
	t.Setenv("FERN_NO_SEM_REACH", "1")
	checkLayoutKernels(t, []layoutCase{{"helper_scale", true}, {"unseeded_scale", false}})
}

func checkLayoutKernels(t *testing.T, cases []layoutCase) {
	t.Helper()
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, dir := compileSourceModload(t, runner, driver, ndarrayLayoutSrc)
	bin := buildBin(t, gcc, dir, "ndarray-layout", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("ndarray layout: %v\n%s", err, out)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := emittedBody(t, string(asm), "__fn_"+tc.name)
			mapped := reachesCall(body, "__fn___smm_ndarray__NdArray_map__")
			vector := strings.Contains(body, "vbroadcastsd")
			if mapped == tc.kernel || vector != tc.kernel {
				t.Fatalf("kernel=%t, map call=%t, vector splat=%t\n%s", tc.kernel, mapped, vector, body)
			}
		})
	}
}

func TestSelfHostNdarrayLayoutValues(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if _, code := runSelfHostFusionProgram(t, target, ndarrayLayoutSrc); code != 0 {
				t.Fatalf("ndarray layout exited %d", code)
			}
		})
	}
}
