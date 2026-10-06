package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const ndarrayReportSrc = `import "std/ndarray";
@noinline function packed(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map((x: f64): f64 => x * 2.0);
}
@noinline function strided(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).transpose().map((x: f64): f64 => x * 2.0);
}
@noinline function row_major(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).slice(0, 1, 2).reshape([3]).map((x: f64): f64 => x * 2.0);
}
@export("fern:ndarray/report", "unknown")
function unknown(a: ndarray.NdArray[f64]): ndarray.NdArray[f64] {
  return a.map((x: f64): f64 => x * 2.0);
}
@noinline function captured(xs: f64[], k: f64): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map((x: f64): f64 => x * k);
}
@noinline function unresolved(xs: f64[], f: (f64) => f64): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map(f);
}
@noinline function added(xs: f64[]): ndarray.NdArray[f64] {
  return ndarray.from_flat(xs, [2, 3]).map((x: f64): f64 => x + 2.0);
}
@noinline function integers(xs: i64[]): ndarray.NdArray[i64] {
  return ndarray.from_flat(xs, [xs.len()]).map((x: i64): i64 => x * (2 as i64));
}
@noinline function axes(a: ndarray.NdArray[f64], axis: i32): ndarray.NdArray[f64] {
  return a.reduce_axis(axis, 0.0, (a: f64, b: f64): f64 => a + b);
}
@noinline function algebra(xs: f64[]): boolean {
  let a: ndarray.NdArray[f64] = ndarray.from_flat(xs, [2, 3]);
  let b: ndarray.NdArray[f64] = a.transpose();
  let zipped: ndarray.NdArray[f64] = a.zip_with(a, (x: f64, y: f64): f64 => x + y);
  let outer: ndarray.NdArray[f64] = a.outer(b, (x: f64, y: f64): f64 => x * y);
  let inner: ndarray.NdArray[f64] = a.inner(b, 0.0, (x: f64, y: f64): f64 => x * y, (x: f64, y: f64): f64 => x + y);
  let folded: f64 = a.fold_all(0.0, (x: f64, y: f64): f64 => x + y);
  let reduced: ndarray.NdArray[f64] = a.reduce_axis(1, 0.0, (x: f64, y: f64): f64 => x + y);
  let scanned: ndarray.NdArray[f64] = a.scan_axis(1, 0.0, (x: f64, y: f64): f64 => x + y);
  let ranked: ndarray.NdArray[f64] = a.map_rank(1, (cell: ndarray.NdArray[f64]): ndarray.NdArray[f64] => cell.map((x: f64): f64 => x * 2.0));
  return zipped.get([1, 2]) == 12.0 && outer.get([1, 2, 2, 1]) == 36.0 && inner.get([0, 0]) == 14.0 && inner.get([1, 1]) == 77.0 && folded == 21.0 && reduced.get([1]) == 15.0 && scanned.get([1, 2]) == 15.0 && ranked.get([1, 2]) == 12.0;
}
function main(): i32 {
  let xs: f64[] = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0];
  let a: ndarray.NdArray[f64] = ndarray.from_flat(xs, [2, 3]);
  if (packed(xs).get([1, 2]) != 12.0 || strided(xs).get([2, 1]) != 12.0) { return 1; }
  if (row_major(xs).get([0]) != 8.0 || unknown(a.transpose()).get([2, 1]) != 12.0) { return 2; }
  if (captured(xs, 2.0).get([1, 2]) != 12.0 || unresolved(xs, (x: f64): f64 => x * 2.0).get([1, 2]) != 12.0) { return 3; }
  if (added(xs).get([1, 2]) != 8.0 || integers([1 as i64, 2 as i64]).get([1]) != 4 as i64) { return 4; }
  if (!algebra(xs) || axes(a, 1).get([1]) != 15.0) { return 5; }
  if (xs[5] != 6.0 || a.get([0, 1]) != 2.0) { return 6; }
  return 0;
}
`

func compileNdarrayReport(t *testing.T, runner []string, driver, src, report, disabled string) (string, string, string) {
	t.Helper()
	dir := e2eharness.WriteSourceModloadProject(t, src)
	cmd := runX86_64Bin(runner, driver, filepath.Join(dir, "main.fern"))
	cmd.Env = append(os.Environ(), "FERN_ARRAY_REPORT="+report, "FERN_NO_SCALE_KERNEL="+disabled)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	asm, err := cmd.Output()
	if err != nil {
		t.Fatalf("compile ndarray report: %v\n%s", err, stderr.String())
	}
	return string(asm), stderr.String(), dir
}

func TestSelfHostNdarrayReportMatchesEmission(t *testing.T) {
	gcc, runner, driver := buildModloadDriverX86(t)
	asm, report, dir := compileNdarrayReport(t, runner, driver, ndarrayReportSrc, "1", "0")
	quietASM, quiet, _ := compileNdarrayReport(t, runner, driver, ndarrayReportSrc, "0", "0")
	if quiet != "" || quietASM != asm {
		t.Fatal("ndarray reporting changed emitted code or printed while disabled")
	}
	for _, tc := range []struct{ name, layout, reason string }{
		{"packed", "packed", "scale-kernel"},
		{"strided", "strided", "layout-strided"},
		{"row_major", "row-major", "layout-row-major"},
		{"unknown", "unknown", "layout-unknown"},
		{"captured", "packed", "element-fn-captures"},
		{"unresolved", "packed", "element-fn-unresolved"},
		{"added", "packed", "element-not-literal-scale"},
		{"integers", "packed", "unsupported-element-type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := strings.Index(report, tc.name+": map; layout="+tc.layout+";")
			if start < 0 {
				t.Fatalf("missing layout for %s:\n%s", tc.name, report)
			}
			decision := strings.SplitN(report[start:], "\n", 3)[1]
			if !strings.Contains(decision, "reason="+tc.reason+" ") {
				t.Fatalf("wrong decision: %s", decision)
			}
			body := emittedBody(t, asm, "__fn_"+tc.name)
			vector := strings.Contains(body, "unpcklpd")
			mapped := strings.Contains(body, "call __fn___smm_ndarray__NdArray_map__")
			kernel := tc.reason == "scale-kernel"
			if vector != kernel || mapped == kernel {
				t.Fatalf("reported %s but vector=%t, mapped=%t\n%s", tc.reason, vector, mapped, body)
			}
		})
	}
	for _, plan := range []string{
		"algebra: zip_with; layout=packed; rhs-layout=packed;",
		"algebra: fold_all; layout=packed;",
		"algebra: reduce_axis; layout=packed; axis=1;",
		"algebra: scan_axis; layout=packed; axis=1;",
		"algebra: map_rank; layout=packed; rank=1;",
		"axes: reduce_axis; layout=packed; axis=?;",
	} {
		start := strings.Index(report, plan)
		if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason=unsupported-operation ") {
			t.Fatalf("missing scalar decision for %s:\n%s", plan, report)
		}
	}
	for _, op := range []string{"outer", "inner"} {
		start := strings.Index(report, "algebra: "+op+"; layout=packed; rhs-layout=strided;")
		if start < 0 || !strings.Contains(strings.SplitN(report[start:], "\n", 3)[1], "reason=layout-strided ") {
			t.Fatalf("missing strided %s refusal:\n%s", op, report)
		}
	}
	bin := buildBin(t, gcc, dir, "ndarray-report", asm)
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("ndarray report program: %v\n%s", err, out)
	}
	_, disabled, _ := compileNdarrayReport(t, runner, driver, ndarrayReportSrc, "1", "1")
	if strings.Contains(disabled, "reason=scale-kernel ") || !strings.Contains(disabled, "reason=disabled ") {
		t.Fatalf("disabled scale kernel reported a rewrite:\n%s", disabled)
	}
	assertArrayReportRefusalsPreserved(t, report, disabled, "ndarray kernels", "scale-kernel")
	shadowSrc := strings.Replace(ndarrayReportSrc, "function main(): i32 {", "function main(): i32 {\n  if (__scale_f64([0.0], 0.0)[0] != 99.0) { return 7; }", 1) + `
function __scale_f64(xs: f64[], k: f64): f64[] { return [99.0]; }
`
	shadowASM, shadow, shadowDir := compileNdarrayReport(t, runner, driver, shadowSrc, "1", "0")
	if strings.Contains(shadow, "reason=scale-kernel ") || !strings.Contains(shadow, "reason=builtin-shadowed ") {
		t.Fatalf("shadowed builtin reported a kernel:\n%s", shadow)
	}
	_, disabledShadow, _ := compileNdarrayReport(t, runner, driver, shadowSrc, "1", "1")
	assertArrayReportRefusalsPreserved(t, shadow, disabledShadow, "ndarray kernels", "scale-kernel")
	shadowBin := buildBin(t, gcc, shadowDir, "ndarray-shadow", shadowASM)
	if out, err := runX86_64Bin(runner, shadowBin).CombinedOutput(); err != nil {
		t.Fatalf("shadowed builtin changed values: %v\n%s", err, out)
	}
}

func TestSelfHostNdarrayReportWithoutScaleInstance(t *testing.T) {
	_, runner, driver := buildModloadDriverX86(t)
	src := `import "std/ndarray";
function main(): i32 {
  let a: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64], [2]);
  return a.map((x: i64): i64 => x + (1 as i64)).len();
}`
	_, report, _ := compileNdarrayReport(t, runner, driver, src, "1", "0")
	if !strings.Contains(report, "main: map; layout=packed;") || !strings.Contains(report, "reason=unsupported-element-type ") {
		t.Fatalf("a program without f64 map lost its ndarray report:\n%s", report)
	}
}

func TestSelfHostNdarrayReportValues(t *testing.T) {
	t.Setenv("FERN_ARRAY_REPORT", "1")
	for _, target := range selfHostFusionTargets {
		t.Run(target, func(t *testing.T) {
			if out, code := runSelfHostFusionProgram(t, target, ndarrayReportSrc); code != 0 {
				t.Fatalf("ndarray report program exited %d\n%s", code, out)
			}
		})
	}
}
