package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// arrayReport writes src as main.fern and returns `fern -array-report`'s output.
func arrayReport(t *testing.T, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runArrayReport(path, &out); err != nil {
		t.Fatalf("runArrayReport: %v", err)
	}
	return out.String()
}

// A three-stage chain is reported as one traversal, with the reason tag that
// fused it (#9730).
func TestArrayReportCLI(t *testing.T) {
	got := arrayReport(t, `import "std/array";
function run(xs: i64[]): i64 {
  let out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .filter((x: i64): boolean => x > (0 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }
`)
	for _, want := range []string{
		"array report: primary Fern compiler, typed semantic fusion",
		"run: map -> filter -> reduce; one traversal",
		"reason=fused storage=no-intermediate-arrays",
		"typed semantic fusion decisions: 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
}

// A program with no std/array chain reports zero decisions rather than
// printing nothing a reader would have to interpret.
func TestArrayReportCLIEmpty(t *testing.T) {
	got := arrayReport(t, "function main(): i32 { return 0; }\n")
	if !strings.Contains(got, "typed semantic fusion decisions: 0") {
		t.Errorf("want the zero-decision line, got:\n%s", got)
	}
}

// A std/ndarray product is reported with its layouts and the element
// functions the site was handed (#9735).
func TestArrayReportCLINdarrayProducts(t *testing.T) {
	got := arrayReport(t, `import "std/ndarray";
function mul(x: i64, y: i64): i64 { return x * y; }
function add(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
  let v: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64], [2]);
  return v.inner(v, 0 as i64, mul, add).get([]) as i32;
}
`)
	for _, want := range []string{
		"array report: primary Fern compiler, ndarray kernels",
		"main: inner; layout=packed; rhs-layout=packed; multiply=mul$wrap; element=add$wrap",
		"ndarray kernels decisions: 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
}

// An axis-parameterized site carries the axis it walks, so the report says
// which elements an operation touches and not only which operation it is.
func TestArrayReportCLINdarrayAxis(t *testing.T) {
	got := arrayReport(t, `import "std/ndarray";
function add(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
  let m: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2]);
  return m.reduce_axis(1, 0 as i64, add).get([0]) as i32;
}
`)
	if !strings.Contains(got, "main: reduce_axis; layout=packed; axis=1; element=add$wrap") {
		t.Errorf("report does not name the axis the reduction walks, over the layout it walks:\n%s", got)
	}
}

// A program the compiler refuses reports the diagnostic, not an empty report.
func TestArrayReportCLICompileError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte("function main(): i32 { return true; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := runArrayReport(path, &out)
	if err == nil {
		t.Fatalf("runArrayReport accepted a program that does not type-check:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "E0") {
		t.Errorf("want the compiler's diagnostic, got: %v", err)
	}
}
