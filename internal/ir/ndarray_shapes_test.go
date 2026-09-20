package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Recognition of std/ndarray's products as whole shapes (#9735, step 3). The
// pass rewrites nothing, so what these lock is what it SEES: each `inner`
// and `outer` site with the element functions it was handed, and — as much
// as the positives — that a user's own `NdArray` is not the algebra's.

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
		"std/ndarray products, recognized by identity and lowered as the scalar loop:",
		"run:5:",
		"inner  mul, add",
		"outer  __closure_lambda",
		"This is the NATIVE compiler's plan.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report does not mention %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "std/ndarray products") > strings.Index(got, "NATIVE compiler's plan") {
		t.Errorf("the products section comes after the compiler note:\n%s", got)
	}
}
