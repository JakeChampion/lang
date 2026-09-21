package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// `fern -array-report` says a stage was replaced by the scale kernel (#9735).
//
// The question the storage section asks is where a stage's buffer came from,
// and "the kernel wrote it" is one of the answers — so this is a new verdict
// in the existing closed set rather than a section of its own.

func storageTags(t *testing.T, body string) map[string]int {
	t.Helper()
	p := lowerPipelineSrc(t, `import "std/array";
`+body)
	out := map[string]int{}
	for _, v := range ir.ArrayStorageVerdicts(p) {
		out[v.Tag()]++
	}
	return out
}

func TestArrayReportNamesTheScaleKernel(t *testing.T) {
	got := storageTags(t, `function main(): i32 {
  var xs: f64[] = [1.0, 2.0, 3.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.0);
  var zs: f64[] = xs.map((x: f64): f64 => x + 1.0);
  return (ys[0] + zs[0]) as i32;
}`)
	if got["scale-kernel"] != 1 {
		t.Errorf("%d stages reported scale-kernel, want 1: %v", got["scale-kernel"], got)
	}
	// The stage beside it is a map the kernel does not take, and it still
	// reports R7's reason rather than being swept into the new verdict.
	if got["receiver-not-own-param"] != 1 {
		t.Errorf("%d stages reported receiver-not-own-param, want 1: %v", got["receiver-not-own-param"], got)
	}
}

// The report may not claim a rewrite the pass did not perform. Both read
// scaleF64Verdict, and this is what holds them to it: the number of stages
// reported as the kernel's is the number of sites the pass rewrites.
func TestArrayReportScaleKernelCountMatchesThePass(t *testing.T) {
	src := `import "std/array";
function half(x: f64): f64 { return x * 0.5; }
function main(): i32 {
  var xs: f64[] = [1.0, 2.0, 3.0];
  var a: f64[] = xs.map((x: f64): f64 => x * 2.0);
  var b: f64[] = xs.map(half);
  var c: f64[] = xs.map((x: f64): f64 => x + 1.0);
  var d: i64[] = [1 as i64];
  var e: i64[] = d.map((x: i64): i64 => x * (2 as i64));
  return (a[0] + b[0] + c[0]) as i32 + (e[0] as i32);
}`
	p := lowerPipelineSrc(t, src)
	reported := 0
	for _, v := range ir.ArrayStorageVerdicts(p) {
		if v == ir.StorageScaleKernel {
			reported++
		}
	}
	// A second lowering, so the pass runs on a program the report has not
	// already been asked about.
	q := lowerPipelineSrc(t, src)
	rewritten := ir.ScaleF64Maps(q)
	if reported != rewritten {
		t.Errorf("the report names %d stages as the kernel's and the pass rewrites %d; "+
			"the report is claiming a rewrite that does not happen (or missing one that does)",
			reported, rewritten)
	}
	if reported != 2 {
		t.Errorf("reported %d kernel stages, want 2 — the lambda and the named function", reported)
	}
}

// R7 runs first and keeps what it takes: a donated buffer is reported as
// donated, never as the kernel's.
func TestArrayReportPrefersR7OverTheKernel(t *testing.T) {
	got := storageTags(t, `function dbl(x: i64): i64 { return x * (2 as i64); }
function twice(own xs: i64[]): i64[] { return xs.map((x: i64): i64 => dbl(x)); }
function main(): i32 {
  var ys: i64[] = twice([1 as i64, 2 as i64]);
  return ys[0] as i32;
}`)
	if got["reused"] != 1 {
		t.Fatalf("%d stages reported reused, want 1 — this test no longer sets up the precedence it tests: %v", got["reused"], got)
	}
	if got["scale-kernel"] != 0 {
		t.Errorf("%d stages reported scale-kernel over a donated buffer, want 0", got["scale-kernel"])
	}
}

// The per-site line and the histogram row both carry it.
func TestArrayReportPrintsTheScaleKernel(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function main(): i32 {
  var xs: f64[] = [1.0, 2.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.0);
  return ys[0] as i32;
}`)
	site := ir.FormatArrayPipelines(p)
	if !strings.Contains(site, "fresh buffer: __fern_scale_f64 replaced the map") {
		t.Errorf("the per-site line does not name the kernel:\n%s", site)
	}
	hist := ir.FormatArrayPipelineHistogram(p)
	if !strings.Contains(hist, "scale-kernel               1") {
		t.Errorf("the histogram does not count the kernel:\n%s", hist)
	}
}
