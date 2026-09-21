package ir_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// What a kernel could do with each element function a recognized
// std/ndarray site was handed (#9735). docs/ARRAY-SHAPES.md §6 says the
// section exists so that "a site whose `mul` and `add` are multiplication
// and addition over `f64` is `dot_f64` in disguise, and one handed a closure
// that captures is not" — these lock that the report answers it rather than
// printing two names and leaving the reader to go and look.

const elementSrc = `import "std/ndarray";
function fmul(x: f64, y: f64): f64 { return x * y; }
function fadd(x: f64, y: f64): f64 { return x + y; }
function scale(x: f64): f64 { return x * 2.0; }
function twice(x: f64): f64 { return (x + x) * 2.0; }
function loud(x: f64): f64 { print("x"); return x; }
function ident(x: f64): f64 { return x; }
function neg(x: f64): f64 { return -x; }
function third(x: i64): i64 { return x / 3; }
function ult(x: u64, y: u64): boolean { return x < y; }
function run(a: ndarray.NdArray[f64], b: ndarray.NdArray[f64], m: ndarray.NdArray[i64], u: ndarray.NdArray[u64]): f64 {
  var d: ndarray.NdArray[f64] = a.inner(b, 0.0, fmul, fadd);
  var s: ndarray.NdArray[f64] = a.map(scale);
  var t: ndarray.NdArray[f64] = a.map(twice);
  var l: ndarray.NdArray[f64] = a.map(loud);
  var i: ndarray.NdArray[f64] = a.map(ident);
  var n: ndarray.NdArray[f64] = a.map(neg);
  var f: f64 = 7.0;
  var c: ndarray.NdArray[f64] = a.map((x: f64): f64 => x * f);
  var h: ndarray.NdArray[i64] = m.map(third);
  var q: ndarray.NdArray[boolean] = u.zip_with(u, ult);
  return d.get([]) + s.get([0]) + t.get([0]) + l.get([0]) + i.get([0]) + n.get([0]) + c.get([0]) + (h.get([0]) as f64) + (if (q.get([0])) { 1.0 } else { 0.0 });
}
function main(): i32 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0], [2]);
  var m: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64], [2]);
  var u: ndarray.NdArray[u64] = ndarray.from_flat([1u64, 2u64], [2]);
  return run(a, a, m, u) as i32;
}`

func elementVerdicts(t *testing.T) []ir.NdarrayKernelVerdict {
	t.Helper()
	p := lowerPipelineSrc(t, elementSrc)
	var out []ir.NdarrayKernelVerdict
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func != "run" {
			continue
		}
		out = append(out, ir.NdarrayKernelVerdicts(p, s)...)
	}
	return out
}

// The positive, and the one that matters most: `inner` over two primitives
// is the shape a dot-product kernel replaces, and the report names both
// operations and their width rather than their identifiers.
func TestNdarrayInnerOverPrimitivesIsNamedAsSuch(t *testing.T) {
	got := elementVerdicts(t)
	if len(got) < 2 {
		t.Fatalf("run's element functions = %+v, want at least two", got)
	}
	if got[0].Why != ir.NdarrayElementPrimitive || got[0].Op != "f64 mul" {
		t.Errorf("inner's mul = %+v, want the primitive f64 mul", got[0])
	}
	if got[1].Why != ir.NdarrayElementPrimitive || got[1].Op != "f64 add" {
		t.Errorf("inner's add = %+v, want the primitive f64 add", got[1])
	}
}

// One operation over the operands is the bar, and a constant operand is
// still one operation: `x * 2.0` is what a scaling kernel emits.
func TestNdarrayElementRefusals(t *testing.T) {
	got := elementVerdicts(t)
	for _, tc := range []struct {
		at   int
		what string
		want ir.NdarrayElementRefusal
		op   string
	}{
		{2, "scale, one multiply by a constant", ir.NdarrayElementPrimitive, "f64 mul"},
		{3, "twice, two operations", ir.NdarrayElementNotOneOp, ""},
		{4, "loud, which calls print", ir.NdarrayElementCalls, ""},
		{5, "ident, which applies nothing", ir.NdarrayElementNotOneOp, ""},
		{6, "neg, a unary a kernel emits as readily as a multiply", ir.NdarrayElementPrimitive, "f64 neg"},
		{7, "a lambda over a captured factor", ir.NdarrayElementCaptures, ""},
		{8, "third, an integer divide, which traps on zero", ir.NdarrayElementNotArithmetic, ""},
		{9, "ult, an unsigned compare", ir.NdarrayElementPrimitive, "u64 lt"},
	} {
		if tc.at >= len(got) {
			t.Fatalf("run has %d element functions, want at least %d: %+v", len(got), tc.at+1, got)
		}
		if got[tc.at].Why != tc.want {
			t.Errorf("%s = %s, want %s", tc.what, got[tc.at].Why.Tag(), tc.want.Tag())
		}
		if got[tc.at].Op != tc.op {
			t.Errorf("%s names the operation %q, want %q", tc.what, got[tc.at].Op, tc.op)
		}
	}
}

// The two verdicts no source shape reaches: an element function the
// recogniser could not name, and one it named that the program does not
// hold. Both are read straight from the classifier, which is the only way to
// reach them without a lowering that produces neither today.
func TestNdarrayElementVerdictsWithoutABody(t *testing.T) {
	p := lowerPipelineSrc(t, elementSrc)
	got := ir.NdarrayKernelVerdicts(p, ir.NdarrayShape{Elements: []ir.NdarrayElementFn{
		{Name: ""},
		{Name: "no_such_function"},
		{Name: "fmul", Captures: 1},
	}})
	for i, want := range []ir.NdarrayElementRefusal{
		ir.NdarrayElementUnresolved,
		ir.NdarrayElementNotInProgram,
		ir.NdarrayElementCaptures,
	} {
		if got[i].Why != want {
			t.Errorf("element %d = %s, want %s", i, got[i].Why.Tag(), want.Tag())
		}
	}
}

// Every refusal has a distinct tag and a reason clause, because the set is
// the coverage checklist for widening the kernels and a duplicate tag would
// merge two rows of it.
func TestNdarrayElementRefusalTagsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	all := append([]ir.NdarrayElementRefusal{ir.NdarrayElementPrimitive}, ir.AllNdarrayElementRefusals...)
	for _, r := range all {
		tag := r.Tag()
		if tag == "unknown" || seen[tag] {
			t.Errorf("refusal %d has the tag %q, which is unusable", int(r), tag)
		}
		seen[tag] = true
		if s := r.String(); s == "" || s == "no reason recorded" {
			t.Errorf("refusal %q has no reason clause", tag)
		}
	}
}

func TestNdarrayElementHistogramCountsEveryReasonAtZero(t *testing.T) {
	p := lowerPipelineSrc(t, elementSrc)
	got := ir.FormatNdarrayElementHistogram(p)
	if !strings.Contains(got, "std/ndarray element functions (#9735): 10, 5 a kernel could inline") {
		t.Errorf("histogram total is not the one this fixture has:\n%s", got)
	}
	for _, want := range []string{
		"element-fn-unresolved      0",
		"element-fn-not-in-program  0",
		"element-fn-captures        1",
		"element-fn-calls           1",
		"element-fn-not-one-op      2",
		"element-fn-not-arithmetic  1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("histogram does not carry %q:\n%s", want, got)
		}
	}
}

// A program with no std/ndarray site prints no section at all rather than an
// empty heading, the way the products list does.
func TestNdarrayElementHistogramIsEmptyWithoutSites(t *testing.T) {
	p := lowerPipelineSrc(t, `function main(): i32 { return 0; }`)
	if got := ir.FormatNdarrayElementHistogram(p); got != "" {
		t.Errorf("histogram over a program with no algebra = %q, want nothing", got)
	}
}

// `x < y` over `u64` is an OpLtS carrying Unsigned, and a kernel has to emit
// it as `lt_u`. Naming it `i64 lt` would name an instruction the site does
// not want, which is the kind of wrong entry the worklist exists to not
// have. Nothing else in the fixture reads Unsigned, so this is its own pin.
func TestNdarrayUnsignedOperationIsNamedUnsigned(t *testing.T) {
	got := elementVerdicts(t)
	last := got[len(got)-1]
	if last.Why != ir.NdarrayElementPrimitive || last.Op != "u64 lt" {
		t.Errorf("the unsigned compare = %+v, want the primitive u64 lt", last)
	}
	for _, v := range got {
		if strings.HasPrefix(v.Op, "i") && strings.HasSuffix(v.Op, " lt") {
			t.Errorf("a compare is named %q; the fixture has no signed one, so Unsigned was dropped", v.Op)
		}
	}
}

// Whether a whole site is a kernel candidate, which is the question a
// planner asks. Every element function primitive is necessary but not
// sufficient: an axis verb also has to say which elements it walks.

func siteVerdicts(t *testing.T, src, fn string) map[string]ir.NdarraySiteRefusal {
	t.Helper()
	p := lowerPipelineSrc(t, src)
	out := map[string]ir.NdarraySiteRefusal{}
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == fn {
			out[fmt.Sprintf("%s:%d", s.Verb, s.Line)] = ir.NdarraySiteVerdict(p, s)
		}
	}
	return out
}

func TestNdarraySiteVerdictNeedsEveryElementAndTheAxis(t *testing.T) {
	got := siteVerdicts(t, algebraSrc, "algebra")
	for _, tc := range []struct {
		site string
		want ir.NdarraySiteRefusal
		why  string
	}{
		{"map:8", ir.NdarraySiteCandidate, "a primitive element function and no axis argument"},
		{"fold_all:10", ir.NdarraySiteCandidate, "a primitive element function and no axis argument"},
		{"reduce_axis:11", ir.NdarraySiteCandidate, "a primitive element function and a literal axis"},
		{"map_rank:13", ir.NdarraySiteElementNotPrimitive, "its cell function calls out"},
		{"reduce_axis:14", ir.NdarraySiteAxisNotLiteral, "the element function is fine and the axis is computed"},
	} {
		v, ok := got[tc.site]
		if !ok {
			t.Fatalf("no site %s among %v", tc.site, got)
		}
		if v != tc.want {
			t.Errorf("%s = %s, want %s (%s)", tc.site, v.Tag(), tc.want.Tag(), tc.why)
		}
	}
}

// The axis refusal is the one that a per-element reading cannot reach: that
// site's element function IS primitive, and only the site-level question
// declines it.
func TestNdarrayAxisRefusalIsNotAnElementRefusal(t *testing.T) {
	p := lowerPipelineSrc(t, algebraSrc)
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func != "algebra" || s.Line != 14 {
			continue
		}
		for _, v := range ir.NdarrayKernelVerdicts(p, s) {
			if v.Why != ir.NdarrayElementPrimitive {
				t.Fatalf("the computed-axis site's element function = %s, so this test no longer isolates the axis", v.Why.Tag())
			}
		}
		if got := ir.NdarraySiteVerdict(p, s); got != ir.NdarraySiteAxisNotLiteral {
			t.Errorf("the computed-axis site = %s, want axis-not-literal", got.Tag())
		}
		return
	}
	t.Fatal("algebraSrc no longer has a site on line 14")
}

func TestNdarraySiteRefusalTagsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	all := append([]ir.NdarraySiteRefusal{ir.NdarraySiteCandidate}, ir.AllNdarraySiteRefusals...)
	for _, r := range all {
		tag := r.Tag()
		if tag == "unknown" || seen[tag] {
			t.Errorf("site refusal %d has the tag %q, which is unusable", int(r), tag)
		}
		seen[tag] = true
		if s := r.String(); s == "" || s == "no reason recorded" {
			t.Errorf("site refusal %q has no reason clause", tag)
		}
	}
}

func TestNdarraySiteHistogramAndReportLine(t *testing.T) {
	p := lowerPipelineSrc(t, algebraSrc)
	hist := ir.FormatNdarraySiteHistogram(p)
	for _, want := range []string{
		"std/ndarray sites (#9735): 8, 6 a kernel could be planned against",
		"element-not-primitive      1",
		"axis-not-literal           1",
	} {
		if !strings.Contains(hist, want) {
			t.Errorf("site histogram does not carry %q:\n%s", want, hist)
		}
	}
	report := ir.FormatNdarrayShapes(p)
	for _, want := range []string{
		"add [i64 add]  -> kernel-candidate",
		"sum_cell [element-fn-calls]  -> element-not-primitive",
		"reduce_axis(axis ?)  add [i64 add]  -> axis-not-literal",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report line does not carry %q:\n%s", want, report)
		}
	}
}

func TestNdarraySiteHistogramIsEmptyWithoutSites(t *testing.T) {
	p := lowerPipelineSrc(t, `function main(): i32 { return 0; }`)
	if got := ir.FormatNdarraySiteHistogram(p); got != "" {
		t.Errorf("site histogram over a program with no algebra = %q, want nothing", got)
	}
}
