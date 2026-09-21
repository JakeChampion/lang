package ir_test

import (
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
function run(a: ndarray.NdArray[f64], b: ndarray.NdArray[f64]): f64 {
  var d: ndarray.NdArray[f64] = a.inner(b, 0.0, fmul, fadd);
  var s: ndarray.NdArray[f64] = a.map(scale);
  var t: ndarray.NdArray[f64] = a.map(twice);
  var l: ndarray.NdArray[f64] = a.map(loud);
  var i: ndarray.NdArray[f64] = a.map(ident);
  var n: ndarray.NdArray[f64] = a.map(neg);
  var f: f64 = 7.0;
  var c: ndarray.NdArray[f64] = a.map((x: f64): f64 => x * f);
  return d.get([]) + s.get([0]) + t.get([0]) + l.get([0]) + i.get([0]) + n.get([0]) + c.get([0]);
}
function main(): i32 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0], [2]);
  return run(a, a) as i32;
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
		{6, "neg, one operation a kernel does not emit", ir.NdarrayElementNotArithmetic, ""},
		{7, "a lambda over a captured factor", ir.NdarrayElementCaptures, ""},
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
	if !strings.Contains(got, "std/ndarray element functions (#9735): 8, 3 a kernel could inline") {
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
