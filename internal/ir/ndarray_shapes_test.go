package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Recognition of std/ndarray's algebra as whole shapes (#9735). The pass
// rewrites nothing, so what these lock is what it SEES: every site of
// docs/ARRAY-SHAPES.md §7's eight operations, with the element functions it
// was handed and the axis it walks, and — as much as the positives — that a
// user's own `NdArray` is not the algebra's, and that an axis the recogniser
// cannot read is reported unread rather than guessed.

const productSrc = `import "std/ndarray";
function mul(x: i64, y: i64): i64 { return x * y; }
function add(x: i64, y: i64): i64 { return x + y; }
function run(a: ndarray.NdArray[i64], b: ndarray.NdArray[i64]): i64 {
  var mm: ndarray.NdArray[i64] = a.inner(b, 0 as i64, mul, add);
  var o: ndarray.NdArray[i64] = a.outer(b, (x: i64, y: i64): i64 => x - y);
  return mm.get([0, 0]) + o.get([0, 0, 0, 0]);
}
function main(): i32 {
  var a: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2]);
  return run(a, a) as i32;
}`

func TestRecognizesInnerAndOuter(t *testing.T) {
	p := lowerPipelineSrc(t, productSrc)
	var got []ir.NdarrayShape
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == "run" {
			got = append(got, s)
		}
	}
	if len(got) != 2 {
		t.Fatalf("recognized %d products in run, want 2: %+v", len(got), got)
	}
	if got[0].Verb != "inner" || got[1].Verb != "outer" {
		t.Errorf("verbs %q, %q; want inner then outer", got[0].Verb, got[1].Verb)
	}
	if got[0].Line != 5 || got[1].Line != 6 {
		t.Errorf("lines %d, %d; want 5 and 6", got[0].Line, got[1].Line)
	}
	if len(got[0].Elements) != 2 || got[0].Elements[0] != "mul" || got[0].Elements[1] != "add" {
		t.Errorf("inner's element functions = %q, want [mul add]", got[0].Elements)
	}
	if len(got[1].Elements) != 1 || !strings.HasPrefix(got[1].Elements[0], "__closure_lambda") {
		t.Errorf("outer's element function = %q, want the lambda", got[1].Elements)
	}
	if !strings.HasPrefix(got[0].Callee, "__method_ndarray__NdArray_inner") {
		t.Errorf("inner's callee = %q", got[0].Callee)
	}
}

// std/ndarray's own bodies are not reported: `outer` is implemented on
// `zip_with`, and neither product calls the other, but the rule is the
// pipeline recogniser's and holds regardless.
func TestNdarrayProductsReportOnlyCallers(t *testing.T) {
	p := lowerPipelineSrc(t, productSrc)
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if strings.HasPrefix(s.Func, "ndarray__") || strings.HasPrefix(s.Func, "__method_ndarray__") {
			t.Errorf("reported a site inside std/ndarray: %+v", s)
		}
	}
}

// A user's own NdArray with its own inner is a different struct with a
// different mangling, and the recogniser has no opinion about it.
func TestUserDeclaredNdArrayInnerIsNotTheAlgebra(t *testing.T) {
	p := lowerPipelineSrc(t, `struct NdArray[T] { data: T[] }
function (a: NdArray[T]) inner[T](b: NdArray[T], init: T, mul: (T, T) => T, add: (T, T) => T): NdArray[T] { return a; }
function mul(x: i64, y: i64): i64 { return x * y; }
function add(x: i64, y: i64): i64 { return x + y; }
function run(a: NdArray[i64]): i64 { return a.inner(a, 0 as i64, mul, add).data.len() as i64; }
function main(): i32 { return run(NdArray[i64] { data: [1 as i64] }) as i32; }`)
	if got := ir.RecognizeNdarrayShapes(p); len(got) != 0 {
		t.Errorf("recognized %d products over a user-declared NdArray, want 0: %+v", len(got), got)
	}
	if !strings.Contains(ir.FormatArrayPipelines(p), "no std/array pipelines") {
		t.Errorf("the report over a program with no algebra lost its empty-case line:\n%s", ir.FormatArrayPipelines(p))
	}
}

func TestNdarrayRecognitionDoesNotMutate(t *testing.T) {
	p := lowerPipelineSrc(t, productSrc)
	before := 0
	for _, fn := range p.Funcs {
		before += len(fn.Ops)
	}
	_ = ir.RecognizeNdarrayShapes(p)
	_ = ir.FormatNdarrayShapes(p)
	after := 0
	for _, fn := range p.Funcs {
		after += len(fn.Ops)
	}
	if before != after {
		t.Errorf("recognition changed the op count: %d -> %d", before, after)
	}
}

// The report carries the products as their own section, after the
// pipelines and before the compiler note, and says what the lowering is.
func TestArrayReportListsNdarrayProducts(t *testing.T) {
	p := lowerPipelineSrc(t, productSrc)
	got := ir.FormatArrayPipelines(p)
	for _, want := range []string{
		"no std/array pipelines",
		"std/ndarray operations, recognized by identity and lowered as the scalar loop:",
		"run:5:",
		"inner  mul, add",
		"outer  __closure_lambda",
		"This is the NATIVE compiler's plan.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "std/ndarray operations") > strings.Index(got, "NATIVE compiler's plan") {
		t.Errorf("the operations section comes after the compiler note:\n%s", got)
	}
}

// The whole algebra, not just the two products: docs/ARRAY-SHAPES.md §7's
// closed list is eight operations, and each one is a site a kernel would
// replace.
const algebraSrc = `import "std/ndarray";
function add(x: i64, y: i64): i64 { return x + y; }
function dbl(x: i64): i64 { return x * (2 as i64); }
function sum_cell(c: ndarray.NdArray[i64]): ndarray.NdArray[i64] {
  return ndarray.from_flat([c.fold_all(0 as i64, add)], []);
}
function algebra(a: ndarray.NdArray[i64], b: ndarray.NdArray[i64], k: i32): i64 {
  var m: ndarray.NdArray[i64] = a.map(dbl);
  var z: ndarray.NdArray[i64] = a.zip_with(b, add);
  var t: i64 = a.fold_all(0 as i64, add);
  var r: ndarray.NdArray[i64] = a.reduce_axis(1, 0 as i64, add);
  var sc: ndarray.NdArray[i64] = a.scan_axis(0, 0 as i64, add);
  var mr: ndarray.NdArray[i64] = a.map_rank(1, sum_cell);
  var q: ndarray.NdArray[i64] = a.reduce_axis(k, 0 as i64, add);
  return m.get([0, 0]) + z.get([0, 0]) + t + r.get([0]) + sc.get([0, 0]) + mr.get([0]) + q.get([0]);
}
function main(): i32 {
  var a: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2]);
  return algebra(a, a, 1) as i32;
}`

func algebraSites(t *testing.T, fn string) []ir.NdarrayShape {
	t.Helper()
	p := lowerPipelineSrc(t, algebraSrc)
	var got []ir.NdarrayShape
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == fn {
			got = append(got, s)
		}
	}
	return got
}

func TestRecognizesTheWholeNdarrayAlgebra(t *testing.T) {
	got := algebraSites(t, "algebra")
	want := []string{"map", "zip_with", "fold_all", "reduce_axis", "scan_axis", "map_rank", "reduce_axis"}
	if len(got) != len(want) {
		t.Fatalf("recognized %d operations, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Verb != w {
			t.Errorf("site %d is %q, want %q", i, got[i].Verb, w)
		}
		if len(got[i].Elements) != 1 || got[i].Elements[0] == "" {
			t.Errorf("site %d (%s) element functions = %q, want one resolved name", i, w, got[i].Elements)
		}
	}
	if got[0].Elements[0] != "dbl" || got[5].Elements[0] != "sum_cell" {
		t.Errorf("map got %q and map_rank got %q; want dbl and sum_cell", got[0].Elements[0], got[5].Elements[0])
	}
}

// A call inside a user function that std/ndarray then calls back is still
// the user's site: `sum_cell` is handed to map_rank and folds its cell.
func TestRecognizesNdarrayOperationInACallback(t *testing.T) {
	got := algebraSites(t, "sum_cell")
	if len(got) != 1 || got[0].Verb != "fold_all" {
		t.Fatalf("sum_cell's sites = %+v, want one fold_all", got)
	}
}

// The axis is the half of an axis-parameterized site a kernel plans
// against: a reduction along the last axis walks contiguous storage and one
// along any other axis strides.
func TestNdarrayAxisArgumentIsRead(t *testing.T) {
	got := algebraSites(t, "algebra")
	for _, tc := range []struct {
		at   int
		verb string
		want int32
	}{
		{0, "map", ir.NdarrayAxisUnknown},
		{2, "fold_all", ir.NdarrayAxisUnknown},
		{3, "reduce_axis", 1},
		{4, "scan_axis", 0},
		{5, "map_rank", 1},
		{6, "reduce_axis", ir.NdarrayAxisUnknown},
	} {
		if got[tc.at].Verb != tc.verb {
			t.Fatalf("site %d is %q, want %q", tc.at, got[tc.at].Verb, tc.verb)
		}
		if got[tc.at].Axis != tc.want {
			t.Errorf("%s at site %d has axis %d, want %d", tc.verb, tc.at, got[tc.at].Axis, tc.want)
		}
	}
}

// An axis the recogniser cannot read is reported as unread rather than
// guessed, so the two cases are distinguishable in the report.
func TestNdarrayReportNamesTheAxisOrSaysItIsUnread(t *testing.T) {
	p := lowerPipelineSrc(t, algebraSrc)
	got := ir.FormatArrayPipelines(p)
	for _, want := range []string{
		"map                  dbl",
		"zip_with             add",
		"fold_all             add",
		"reduce_axis(axis 1)  add",
		"scan_axis(axis 0)    add",
		"map_rank(rank 1)     sum_cell",
		"reduce_axis(axis ?)  add",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
}

func TestNdarrayAlgebraRecognitionDoesNotMutate(t *testing.T) {
	p := lowerPipelineSrc(t, algebraSrc)
	before := 0
	for _, fn := range p.Funcs {
		before += len(fn.Ops)
	}
	_ = ir.FormatNdarrayShapes(p)
	after := 0
	for _, fn := range p.Funcs {
		after += len(fn.Ops)
	}
	if before != after {
		t.Errorf("recognition changed the op count: %d -> %d", before, after)
	}
}

// A receiver that pushes an integer of its own does not displace the axis.
// `bx.a` puts the field offset 0 in the window before the axis, and the
// `add` that applies that offset is what ends the expression it belongs to,
// so the site reads 1 — the axis as written — rather than 0.
func TestNdarrayAxisOfAFieldReceiverIsNotMisread(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
struct Box { a: ndarray.NdArray[i64] }
function add(x: i64, y: i64): i64 { return x + y; }
function run(bx: Box): i64 {
  return bx.a.reduce_axis(1, 0 as i64, add).get([0]);
}
function main(): i32 {
  var a: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2]);
  return run(Box { a: a }) as i32;
}`)
	var got []ir.NdarrayShape
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == "run" {
			got = append(got, s)
		}
	}
	if len(got) != 1 || got[0].Verb != "reduce_axis" {
		t.Fatalf("run's sites = %+v, want one reduce_axis", got)
	}
	if got[0].Axis != 1 {
		t.Errorf("reduce_axis under a field receiver reports axis %d, want 1; 0 is the field offset, not the axis", got[0].Axis)
	}
}

// A negative axis is out of range, so the site aborts before any kernel
// could run — and `-1` reaches the IR as a `0` and a `1` a `sub` combines,
// which is exactly the shape that would report axis 0 if the run of pushes
// did not start after that `sub`.
func TestNdarrayNegativeAxisIsReportedUnread(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function add(x: i64, y: i64): i64 { return x + y; }
function run(a: ndarray.NdArray[i64]): i64 {
  return a.reduce_axis(-1, 0 as i64, add).get([0]);
}
function main(): i32 {
  var a: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64], [2]);
  return run(a) as i32;
}`)
	var got []ir.NdarrayShape
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == "run" {
			got = append(got, s)
		}
	}
	if len(got) != 1 || got[0].Verb != "reduce_axis" {
		t.Fatalf("run's sites = %+v, want one reduce_axis", got)
	}
	if got[0].Axis != ir.NdarrayAxisUnknown {
		t.Errorf("reduce_axis(-1) reports axis %d, want it unread", got[0].Axis)
	}
}

// A computed axis beside a literal init of the SAME width is the shape that
// catches a recogniser reading "the first integer literal": over an i32
// array, `a.reduce_axis(k, 0, add)` puts the init's `0` where a scan looking
// for a literal finds it first, and reporting that as axis 0 is the
// wrong-axis case the report must never produce. The literal-axis site
// beside it has to keep reading 1.
const i32AxisSrc = `import "std/ndarray";
function add(x: i32, y: i32): i32 { return x + y; }
function run(a: ndarray.NdArray[i32], k: i32): i32 {
  var computed: ndarray.NdArray[i32] = a.reduce_axis(k, 0, add);
  var written: ndarray.NdArray[i32] = a.reduce_axis(1, 0, add);
  var scanned: ndarray.NdArray[i32] = a.scan_axis(k, 0, add);
  return computed.get([0]) + written.get([0]) + scanned.get([0, 0]);
}
function main(): i32 {
  var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
  return run(a, 0);
}`

func TestNdarrayComputedAxisIsNotReadFromTheInit(t *testing.T) {
	p := lowerPipelineSrc(t, i32AxisSrc)
	var got []ir.NdarrayShape
	for _, s := range ir.RecognizeNdarrayShapes(p) {
		if s.Func == "run" {
			got = append(got, s)
		}
	}
	if len(got) != 3 {
		t.Fatalf("run's sites = %+v, want three", got)
	}
	if got[0].Axis != ir.NdarrayAxisUnknown {
		t.Errorf("reduce_axis(k, 0, add) reports axis %d; the 0 it read is the init, and the axis is k", got[0].Axis)
	}
	if got[1].Axis != 1 {
		t.Errorf("reduce_axis(1, 0, add) reports axis %d, want 1", got[1].Axis)
	}
	if got[2].Axis != ir.NdarrayAxisUnknown {
		t.Errorf("scan_axis(k, 0, add) reports axis %d; it shares reduce_axis's signature and its answer", got[2].Axis)
	}
}
