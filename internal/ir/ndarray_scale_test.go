package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The first kernel an std/ndarray shape lowers to (#9735), and the first time
// #9734's layout analysis decides a REWRITE rather than a report.

const ndScalePacked = `import "std/ndarray";
function run(k: f64): f64 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0], [2, 2]);
  var m: ndarray.NdArray[f64] = a.map((x: f64): f64 => x * k);
  return m.get([0, 0]);
}
function main(): i32 { return run(2.0) as i32; }`

const ndScaleStrided = `import "std/ndarray";
function run(k: f64): f64 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0], [2, 2]);
  var t: ndarray.NdArray[f64] = a.transpose();
  var m: ndarray.NdArray[f64] = t.map((x: f64): f64 => x * k);
  return m.get([0, 0]);
}
function main(): i32 { return run(2.0) as i32; }`

func ndScaleCalls(p *ir.Program, fn string) []string {
	var out []string
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for _, op := range f.Ops {
			if op.Kind == ir.OpCallDirect {
				out = append(out, op.Str)
			}
		}
	}
	return out
}

func hasCall(calls []string, want string) bool {
	for _, c := range calls {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// A packed receiver takes the kernel: the scalar walk goes and the vectorised
// call plus the handle it rebuilds arrive.
func TestNdarrayScaleTakesAPackedReceiver(t *testing.T) {
	p := lowerPipelineSrc(t, ndScalePacked)
	if n := ir.ScaleF64NdarrayMaps(p, 8); n != 1 {
		t.Fatalf("rewrote %d sites, want 1", n)
	}
	calls := ndScaleCalls(p, "run")
	if hasCall(calls, "__method_ndarray__NdArray_map__") {
		t.Errorf("the scalar map survived the rewrite: %v", calls)
	}
	if !hasCall(calls, "__fern_scale_f64") {
		t.Errorf("no kernel call emitted: %v", calls)
	}
	if !hasCall(calls, "ndarray__from_flat__f64") {
		t.Errorf("the result handle is not rebuilt: %v", calls)
	}
}

// A strided receiver does not, and this is the arm that matters: `data` is
// not the reading order there, so the kernel would compute different numbers
// rather than the same ones faster. internal/e2e's case 30 is the same fact
// measured end to end.
func TestNdarrayScaleDeclinesAStridedReceiver(t *testing.T) {
	p := lowerPipelineSrc(t, ndScaleStrided)
	if n := ir.ScaleF64NdarrayMaps(p, 8); n != 0 {
		t.Fatalf("rewrote %d sites over a transpose, want 0", n)
	}
	if !hasCall(ndScaleCalls(p, "run"), "__method_ndarray__NdArray_map__") {
		t.Errorf("the scalar map was removed from a site the kernel may not take")
	}
}

// The rewrite is the only thing that changes: a program with no ndarray map
// by a scalar is left alone, so the pass cannot be the reason something else
// moved.
func TestNdarrayScaleLeavesOtherProgramsAlone(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function add(x: f64, y: f64): f64 { return x + y; }
function run(): f64 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0], [2]);
  return a.fold_all(0.0, add);
}
function main(): i32 { return run() as i32; }`)
	before := 0
	for _, fn := range p.Funcs {
		before += len(fn.Ops)
	}
	if n := ir.ScaleF64NdarrayMaps(p, 8); n != 0 {
		t.Fatalf("rewrote %d sites in a program with no scalar map, want 0", n)
	}
	after := 0
	for _, fn := range p.Funcs {
		after += len(fn.Ops)
	}
	if before != after {
		t.Errorf("op count moved with no rewrite: %d -> %d", before, after)
	}
}

// A NAMED element function, which reaches the call by a different route and
// leaves by one too: it is pushed straight from its `const_func` and never
// parked, so the replaced range ends at the call rather than past a closure
// release. A range end that overshot would delete an op the call still needs
// and hand the verifier a stack imbalance, which is why this shape is pinned
// separately rather than assumed to follow from the lambda one.
func TestNdarrayScaleTakesANamedElementFunction(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function half(x: f64): f64 { return x * 0.5; }
function run(): f64 {
  var a: ndarray.NdArray[f64] = ndarray.from_flat([1.0, 2.0, 3.0, 4.0], [2, 2]);
  var m: ndarray.NdArray[f64] = a.map(half);
  return m.get([0, 0]);
}
function main(): i32 { return run() as i32; }`)
	if n := ir.ScaleF64NdarrayMaps(p, 8); n != 1 {
		t.Fatalf("rewrote %d sites for a named element function, want 1", n)
	}
	calls := ndScaleCalls(p, "run")
	if hasCall(calls, "__method_ndarray__NdArray_map__") {
		t.Errorf("the scalar map survived: %v", calls)
	}
	if !hasCall(calls, "__fern_scale_f64") || !hasCall(calls, "ndarray__from_flat__f64") {
		t.Errorf("kernel or handle rebuild missing: %v", calls)
	}
}
