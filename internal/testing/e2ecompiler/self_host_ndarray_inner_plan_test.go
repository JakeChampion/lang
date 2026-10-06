package e2ecompiler

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ndarrayInnerPlanSrc = `import "std/ndarray";
@noinline function packed(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
}
@noinline function noncanonical(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [1, 4]).reverse(0).packed();
  if (a.strides()[0] != -4) { exit(91); }
  let b = ndarray.from_flat(xs, [4, 1]).reverse(1).packed();
  if (b.strides()[1] != -1) { exit(92); }
  return a.inner(b, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
}
@noinline function reverse_mul(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => y * x, (x: f64, y: f64): f64 => x + y);
}
@noinline function reverse_add(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => y + x);
}
@noinline function extra_add(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y + 1.0);
}
@noinline function capture_mul(xs: f64[], k: f64): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y * k, (x: f64, y: f64): f64 => x + y);
}
@noinline function capture_add(xs: f64[], k: f64): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => (x + y) * k);
}
@noinline function left_strided(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.transpose().inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
}
@noinline function right_strided(xs: f64[]): ndarray.NdArray[f64] {
  let a = ndarray.from_flat(xs, [2, 2]);
  return a.inner(a.transpose(), 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
}
@noinline pub function unknown(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.inner(a, 0.25, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
}
@noinline function uncertain(f: () => ndarray.NdArray[f64]): ndarray.NdArray[f64] { return f(); }
function main(): i32 {
  let xs: f64[] = [1.0, 2.0, 3.0, 4.0];
  if (packed(xs).get([0, 0]) != 7.25) { return 1; }
  if (reverse_mul(xs).get([0, 0]) != 7.25 || reverse_add(xs).get([0, 0]) != 7.25) { return 2; }
  if (extra_add(xs).get([0, 0]) != 9.25) { return 3; }
  if (capture_mul(xs, 2.0).get([0, 0]) != 14.25 || capture_add(xs, 2.0).get([0, 0]) != 17.0) { return 4; }
  if (left_strided(xs).get([0, 0]) != 10.25 || right_strided(xs).get([0, 0]) != 5.25) { return 5; }
  if (unknown(uncertain((): ndarray.NdArray[f64] => ndarray.from_flat(xs, [2, 2]))).get([0, 0]) != 7.25) { return 6; }
  if (unknown(ndarray.from_flat(xs, [2, 2]).transpose()).get([0, 0]) != 7.25) { return 7; }
  let canonical = noncanonical(xs);
  if (canonical.get([0, 0]) != 30.25 || canonical.strides()[0] != 1 || canonical.strides()[1] != 1) { return 8; }
  return 0;
}`

func TestSelfHostNdarrayInnerPlan(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, report, dir := compileProductReport(t, runner, driver, ndarrayInnerPlanSrc, "1", "0")
	quietASM, quiet, _ := compileProductReport(t, runner, driver, ndarrayInnerPlanSrc, "0", "0")
	if asm != quietASM || quiet != "" {
		t.Fatal("inner report changed code or printed while disabled")
	}
	for _, tc := range []struct{ name, reason string }{
		{"packed", "inner-mul-add-kernel"},
		{"noncanonical", "inner-mul-add-kernel"},
		{"reverse_mul", "element-not-binary-multiply"},
		{"reverse_add", "element-not-binary-add"},
		{"extra_add", "element-not-binary-add"},
		{"capture_mul", "element-fn-captures"},
		{"capture_add", "element-fn-captures"},
		{"left_strided", "layout-strided"},
		{"right_strided", "layout-strided"},
		{"unknown", "layout-unknown"},
	} {
		start := strings.Index(report, tc.name+": inner;")
		if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason="+tc.reason+" ") {
			t.Fatalf("missing %s reason %s:\n%s", tc.name, tc.reason, report)
		}
		body := emittedBody(t, asm, "__fn_"+tc.name)
		if tc.reason == "inner-mul-add-kernel" {
			if !strings.Contains(body, "call __fn_ndarray__inner_kernel") || strings.Contains(body, "call __fn___smm_ndarray__NdArray_inner") {
				t.Fatalf("admitted inner kept its scalar call:\n%s", body)
			}
		} else if !strings.Contains(body, "call __fn___smm_ndarray__NdArray_inner") {
			t.Fatalf("refused inner lost its scalar call:\n%s", body)
		}
	}
	disabledASM, disabled, _ := compileProductReport(t, runner, driver, ndarrayInnerPlanSrc, "1", "1")
	assertArrayReportRefusalsPreserved(t, report, disabled, "ndarray kernels", "inner-mul-add-kernel")
	if strings.Contains(disabled, "reason=inner-mul-add-kernel ") || !strings.Contains(disabled, "reason=disabled ") {
		t.Fatalf("disabled inner report:\n%s", disabled)
	}
	if !strings.Contains(emittedBody(t, disabledASM, "__fn_packed"), "call __fn___smm_ndarray__NdArray_inner") {
		t.Fatal("disabled inner kernel lost its scalar call")
	}
	if strings.Contains(disabledASM, "__fn_ndarray__inner_kernel:") {
		t.Fatal("disabled inner kernel retained its unused adapter")
	}
	bin := buildBin(t, gcc, dir, "inner-plan", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("inner plan: %v\n%s", err, out)
	}
	shadowSrc := ndarrayInnerPlanSrc + `
function __inner_mul_add_f64(a: f64[], b: f64[], rows: i32, extent: i32, columns: i32, init: f64): f64[] { return [99.0]; }
`
	shadowASM, shadow, shadowDir := compileProductReport(t, runner, driver, shadowSrc, "1", "0")
	if strings.Contains(shadow, "reason=inner-mul-add-kernel ") || !strings.Contains(shadow, "reason=builtin-shadowed ") {
		t.Fatalf("shadowed inner reported a kernel:\n%s", shadow)
	}
	if strings.Contains(shadowASM, "__fn_ndarray__inner_kernel:") {
		t.Fatal("shadowed inner retained its unused adapter")
	}
	_, shadowDisabled, _ := compileProductReport(t, runner, driver, shadowSrc, "1", "1")
	assertArrayReportRefusalsPreserved(t, shadow, shadowDisabled, "ndarray kernels", "inner-mul-add-kernel")
	shadowBin := buildBin(t, gcc, shadowDir, "inner-shadow-plan", shadowASM)
	if out, err := runX86_64Bin(runner, shadowBin).CombinedOutput(); err != nil {
		t.Fatalf("shadowed inner changed values: %v\n%s", err, out)
	}
}

func TestSelfHostNdarrayInnerSelectedBits(t *testing.T) {
	src := strings.Replace(innerMulAddSource(),
		"ndarray.from_flat(array.inner_mul_add_f64(xs, ys, rows, extent, columns, init), [rows, columns])",
		"a.inner(b, init, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y)", 1)
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, disabled := range []string{"0", "1"} {
				bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_LEAKCHECK=1", "FERN_NO_PRODUCT_KERNEL=" + disabled})
				out, err := runScaleTarget(t, target, bin).CombinedOutput()
				if err != nil {
					t.Fatalf("selected inner disabled=%s: %v\n%s", disabled, err, out)
				}
				var allocs, frees, live int64
				if _, err := fmtSscan(leakSummaryLine(string(out)), &allocs, &frees, &live); err != nil || allocs == 0 || allocs != frees || live != 0 {
					t.Fatalf("selected inner ownership: %v\n%s", err, out)
				}
			}
		})
	}
}

func TestSelfHostNdarrayInnerShapeChecks(t *testing.T) {
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct{ name, a, b string }{
				{"left rank", "ndarray.from_flat([1.0], [])", "ndarray.from_flat([1.0], [1])"},
				{"right rank", "ndarray.from_flat([1.0], [1])", "ndarray.from_flat([1.0], [])"},
				{"contraction", "ndarray.from_flat([1.0, 2.0], [2])", "ndarray.from_flat([1.0], [1])"},
				{"prefix overflow", "ndarray.from_flat([] as f64[], [2, 0])", "ndarray.from_flat([] as f64[], [0, 2147483647, 0])"},
			} {
				for _, disabled := range []string{"0", "1"} {
					src := `import "std/ndarray";
function main(): i32 {
  let a = ` + tc.a + `;
  let b = ` + tc.b + `;
  return a.inner(b, 0.0, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y).len();
}`
					bin := e2eharness.CompileSelfHostSource(t, target, src, []string{"FERN_NO_PRODUCT_KERNEL=" + disabled})
					cmd := runScaleTarget(t, target, bin)
					out, err := cmd.CombinedOutput()
					if target == e2eharness.TargetWasm32Wasi {
						if err == nil || !strings.Contains(string(out), "invalid exit status") {
							t.Fatalf("%s disabled=%s: expected shape exit: %v\n%s", tc.name, disabled, err, out)
						}
					} else if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 134 {
						t.Fatalf("%s disabled=%s: expected exit134: %v\n%s", tc.name, disabled, err, out)
					}
				}
			}
		})
	}
}
