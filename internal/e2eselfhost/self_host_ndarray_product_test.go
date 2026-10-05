package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func ndarrayProductBitsSource() string {
	src := strings.Replace(outerMulSrc, `import "std/array";`, `import "std/ndarray";`, 1)
	return strings.Replace(src, "return array.outer_mul_f64(a, b);", `
  let left = ndarray.from_flat(a, [a.len()]);
  let right = ndarray.from_flat(b, [b.len()]);
  let product = left.outer(right, (x: f64, y: f64): f64 => x * y);
  if (product.shape().len() != 2 || product.shape()[0] != a.len() || product.shape()[1] != b.len() || !product.is_packed()) { exit(90); }
  return product.to_flat();`, 1)
}

func TestSelfHostNdarrayProductBits(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				t.Run("disabled="+disabled, func(t *testing.T) {
					bin := e2eharness.CompileSelfHostSource(t, target, ndarrayProductBitsSource(), []string{"FERN_LEAKCHECK=1", "FERN_NO_PRODUCT_KERNEL=" + disabled})
					out, err := runScaleTarget(t, target, bin).CombinedOutput()
					if err != nil {
						t.Fatalf("ndarray product: %v\n%s", err, out)
					}
					var allocs, frees, live int64
					line := leakSummaryLine(string(out))
					if _, err := fmtSscan(line, &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
						t.Fatalf("ndarray product ownership: %v\n%s", err, out)
					}
				})
			}
		})
	}
}

const ndarrayProductReportSrc = `import "std/ndarray";
@noinline function packed(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a, (x: f64, y: f64): f64 => x * y);
}
@noinline function left_strided(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.transpose().outer(a, (x: f64, y: f64): f64 => x * y);
}
@noinline function right_strided(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a.transpose(), (x: f64, y: f64): f64 => x * y);
}
@noinline function reversed(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a, (x: f64, y: f64): f64 => y * x);
}
@noinline function added(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a, (x: f64, y: f64): f64 => x * y + 1.0);
}
@noinline function captured(xs: f64[], k: f64): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a, (x: f64, y: f64): f64 => x * y * k);
}
@noinline function unresolved(xs: f64[], f: (f64, f64) => f64): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 3]);
  return a.outer(a, f);
}
@noinline function noncanonical(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [1, 6]).reverse(0).packed();
  if (a.strides()[0] != -6) { exit(91); }
  return a.outer(a, (x: f64, y: f64): f64 => x * y);
}
function main(): i32 {
  let xs: f64[] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0];
  if (packed(xs).get([1, 2, 1, 2]) != 36.0) { return 1; }
  if (left_strided(xs).get([2, 1, 1, 2]) != 36.0) { return 2; }
  if (right_strided(xs).get([1, 2, 2, 1]) != 36.0) { return 3; }
  if (reversed(xs).get([0, 1, 0, 2]) != 6.0) { return 4; }
  if (added(xs).get([0, 1, 0, 2]) != 7.0) { return 5; }
  if (captured(xs, 2.0).get([0, 1, 0, 2]) != 12.0) { return 6; }
  if (unresolved(xs, (x: f64, y: f64): f64 => x * y).get([0, 1, 0, 2]) != 6.0) { return 7; }
  let scalar = ndarray.from_flat([2.0], []);
  let one = ndarray.from_flat([3.0], [1]);
  let out = scalar.outer(one, (x: f64, y: f64): f64 => x * y);
  if (out.shape().len() != 1 || out.shape()[0] != 1 || out.get([0]) != 6.0 || !out.is_packed()) { return 8; }
  let canonical = noncanonical(xs);
  if (canonical.get([0, 1, 0, 2]) != 6.0 || canonical.strides()[0] != 36 || canonical.strides()[1] != 6 || canonical.strides()[2] != 6 || canonical.strides()[3] != 1) { return 9; }
  return 0;
}`

func compileProductReport(t *testing.T, runner []string, driver, src, report, disabled string) (string, string, string) {
	t.Helper()
	dir := e2eharness.WriteSourceModloadProject(t, src)
	cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
	cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT="+report, "FERN_NO_PRODUCT_KERNEL="+disabled)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("compile product report: %v\n%s", err, stderr.String())
	}
	return string(asm), stderr.String(), dir
}

func TestSelfHostNdarrayProductReport(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, report, dir := compileProductReport(t, runner, driver, ndarrayProductReportSrc, "1", "0")
	quietASM, quiet, _ := compileProductReport(t, runner, driver, ndarrayProductReportSrc, "0", "0")
	if quiet != "" || quietASM != asm {
		t.Fatal("product reporting changed emitted code or printed while disabled")
	}
	for _, tc := range []struct{ name, reason string }{
		{"packed", "outer-mul-kernel"},
		{"noncanonical", "outer-mul-kernel"},
		{"left_strided", "layout-strided"},
		{"right_strided", "layout-strided"},
		{"reversed", "element-not-binary-multiply"},
		{"added", "element-not-binary-multiply"},
		{"captured", "element-fn-captures"},
		{"unresolved", "element-fn-unresolved"},
	} {
		start := strings.Index(report, tc.name+": outer;")
		if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason="+tc.reason+" ") {
			t.Fatalf("missing %s reason %s:\n%s", tc.name, tc.reason, report)
		}
		body := emittedBody(t, asm, "__fn_"+tc.name)
		kernel := tc.reason == "outer-mul-kernel"
		if strings.Contains(body, "mulpd") != kernel || strings.Contains(body, "call __fn___smm_ndarray__NdArray_outer__") == kernel {
			t.Fatalf("report disagrees with emitted %s:\n%s", tc.name, body)
		}
		if kernel {
			check := strings.Index(body, "call __fn_ndarray__check_shape")
			alloc := strings.Index(body, "call __fern_arr_box")
			if check < 0 || alloc < check {
				t.Fatalf("shape must be checked before kernel allocation:\n%s", body)
			}
		}
	}
	bin := buildBin(t, gcc, dir, "product-report", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("product report program: %v\n%s", err, out)
	}
	disabledASM, disabled, _ := compileProductReport(t, runner, driver, ndarrayProductReportSrc, "1", "1")
	assertArrayReportRefusalsPreserved(t, report, disabled, "ndarray kernels", "outer-mul-kernel")
	if !strings.Contains(disabled, "reason=disabled ") || strings.Contains(disabled, "reason=outer-mul-kernel ") {
		t.Fatalf("disabled product reported a rewrite:\n%s", disabled)
	}
	if body := emittedBody(t, disabledASM, "__fn_packed"); strings.Contains(body, "mulpd") || !strings.Contains(body, "call __fn___smm_ndarray__NdArray_outer__") {
		t.Fatalf("disabled product still rewrote packed:\n%s", body)
	}
	shadowSrc := strings.Replace(ndarrayProductReportSrc, "function main(): i32 {", "function main(): i32 {\n  if (__outer_mul_f64([2.0], [3.0])[0] != 99.0) { return 10; }", 1) + `
function __outer_mul_f64(a: f64[], b: f64[]): f64[] { return [99.0]; }
`
	shadowASM, shadow, shadowDir := compileProductReport(t, runner, driver, shadowSrc, "1", "0")
	if strings.Contains(shadow, "reason=outer-mul-kernel ") || !strings.Contains(shadow, "reason=builtin-shadowed ") {
		t.Fatalf("shadowed product reported a kernel:\n%s", shadow)
	}
	_, shadowDisabled, _ := compileProductReport(t, runner, driver, shadowSrc, "1", "1")
	assertArrayReportRefusalsPreserved(t, shadow, shadowDisabled, "ndarray kernels", "outer-mul-kernel")
	shadowBin := buildBin(t, gcc, shadowDir, "product-shadow", shadowASM)
	if out, err := runX86_64Bin(runner, shadowBin).CombinedOutput(); err != nil {
		t.Fatalf("shadowed kernel changed product values: %v\n%s", err, out)
	}
}

func TestSelfHostNdarrayProductValues(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				bin := e2eharness.CompileSelfHostSource(t, target, ndarrayProductReportSrc, []string{"FERN_NO_PRODUCT_KERNEL=" + disabled})
				if out, err := runScaleTarget(t, target, bin).CombinedOutput(); err != nil {
					t.Fatalf("product values disabled=%s: %v\n%s", disabled, err, out)
				}
			}
		})
	}
}

func TestSelfHostNdarrayProductShapeOverflow(t *testing.T) {
	const src = `import "std/ndarray";
function main(): i32 {
  let a = ndarray.from_flat([1.0, 2.0], [2]);
  let b = ndarray.from_flat([] as f64[], [2147483647, 0]);
  // Both inputs are valid and empty flat output would fit, but the joined
  // shape overflows before reaching the zero axis. Keep the scalar error.
  let out = a.outer(b, (x: f64, y: f64): f64 => x * y);
  return out.len();
}`
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_NO_PRODUCT_KERNEL=" + disabled})
				cmd := runScaleTarget(t, target, bin)
				out, err := cmd.CombinedOutput()
				if target == e2eharness.TargetWasm32Wasi {
					// check_shape calls exit(134); WASI rejects that process exit
					// code. This differs from the flat kernel's unreachable trap.
					if err == nil || !strings.Contains(string(out), "invalid exit status") {
						t.Fatalf("expected rejected shape exit (disabled=%s): %v\n%s", disabled, err, out)
					}
				} else if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
					t.Fatalf("expected shape exit 134 (disabled=%s): %v\n%s", disabled, err, out)
				}
			}
		})
	}
}
