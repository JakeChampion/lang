package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The scale kernel rewrite (#9735, step 4). These lock what it TAKES and what
// it leaves; internal/e2e/array_scale_kernel_test.go locks that what it takes
// still computes the same thing, which no test here can show.

func scaleProgram(t *testing.T, body string) (*ir.Program, int) {
	t.Helper()
	p := lowerPipelineSrc(t, `import "std/array";
`+body)
	return p, ir.ScaleF64Maps(p)
}

// The op stream a taken site leaves: the receiver push the range never
// touched, the constant read out of the element function, and the kernel.
func mainOps(t *testing.T, p *ir.Program) []ir.Op {
	t.Helper()
	for _, fn := range p.Funcs {
		if fn.Name == "main" {
			return fn.Ops
		}
	}
	t.Fatal("no main")
	return nil
}

func TestScaleKernelTakesAConstantMultiply(t *testing.T) {
	p, n := scaleProgram(t, `function main(): i32 {
  var xs: f64[] = [1.0, 2.0, 3.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.5);
  return (ys[0] + ys[2]) as i32;
}`)
	if n != 1 {
		t.Fatalf("rewrote %d sites, want 1", n)
	}
	ops := mainOps(t, p)
	var sawKernel bool
	for i, op := range ops {
		if op.Kind != ir.OpCallDirect || op.Str != "__fern_scale_f64" {
			continue
		}
		sawKernel = true
		if !op.Runtime || op.I32 != 2 {
			t.Errorf("the kernel call is %+v; want a 2-argument runtime call", op)
		}
		if prev := ops[i-1]; prev.Kind != ir.OpConstF64 || prev.F64 != 2.5 {
			t.Errorf("the operand before the kernel is %+v, want the constant 2.5", prev)
		}
		if len(op.ArgTypes()) != 2 {
			t.Errorf("the kernel call carries %d argument types, want 2 — a backend that expands operands reads them", len(op.ArgTypes()))
		}
	}
	if !sawKernel {
		t.Errorf("no __fern_scale_f64 call after a rewrite that reported one")
	}
	for _, op := range ops {
		if op.Kind == ir.OpCallDirect && strings.HasPrefix(op.Str, "__method_Array_map") {
			t.Errorf("the map call survived the rewrite")
		}
		if op.Kind == ir.OpMakeClosure {
			t.Errorf("the element function is still built, so its release has nothing to pair with")
		}
		if op.Kind == ir.OpCallDirect && op.Str == "__drop_closure_value" {
			t.Errorf("a closure release survived with no closure to release")
		}
	}
}

// A named element function reaches the call through OpConstFunc and has no
// release, so the replaced range ends at the call rather than past it.
// A negative literal reaches the IR as the positive constant and a negate,
// so a reading that only accepted a bare constant declined `x * -1.5` — and
// declined it SILENTLY, which made the e2e suite's negative-factor case
// compare the scalar loop with itself.
func TestScaleKernelTakesANegativeFactor(t *testing.T) {
	p, n := scaleProgram(t, `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * -1.5);
  return ys[0] as i32;
}`)
	if n != 1 {
		t.Fatalf("rewrote %d sites for a negative factor, want 1", n)
	}
	for i, op := range mainOps(t, p) {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_scale_f64" {
			if prev := mainOps(t, p)[i-1]; prev.Kind != ir.OpConstF64 || prev.F64 != -1.5 {
				t.Errorf("the kernel's factor is %+v, want the constant -1.5", prev)
			}
		}
	}
}

// The negate has to belong to the constant. `(-x) * 1.5` is a different body
// and the reading must not fold its negate into the factor.
func TestScaleKernelDeclinesANegatedElement(t *testing.T) {
	if _, n := scaleProgram(t, `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => (0.0 - x) * 1.5);
  return ys[0] as i32;
}`); n != 0 {
		t.Errorf("rewrote %d sites where the element itself is negated, want none", n)
	}
}

func TestScaleKernelTakesANamedElementFunction(t *testing.T) {
	p, n := scaleProgram(t, `function half(x: f64): f64 { return x * 0.5; }
function main(): i32 {
  var xs: f64[] = [4.0, 8.0];
  var ys: f64[] = xs.map(half);
  return (ys[0] + ys[1]) as i32;
}`)
	if n != 1 {
		t.Fatalf("rewrote %d sites, want 1", n)
	}
	for _, op := range mainOps(t, p) {
		if op.Kind == ir.OpConstFunc {
			t.Errorf("the element function is still pushed after the rewrite")
		}
	}
}

// Each condition, declined on its own. Refusal is the safe direction: a site
// this pass does not take runs the scalar loop, which is what it ran before.
func TestScaleKernelDeclines(t *testing.T) {
	for _, tc := range []struct {
		why  string
		body string
	}{
		{"the factor is captured rather than constant", `function main(): i32 {
  var k: f64 = 2.0;
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * k);
  return ys[0] as i32;
}`},
		{"the factor is the element itself", `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * x);
  return ys[0] as i32;
}`},
		{"the operation is not a multiply", `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x + 2.0);
  return ys[0] as i32;
}`},
		{"the body is more than one operation", `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.0 + 1.0);
  return ys[0] as i32;
}`},
		{"the elements are not f64", `function main(): i32 {
  var xs: i64[] = [1 as i64];
  var ys: i64[] = xs.map((x: i64): i64 => x * (2 as i64));
  return ys[0] as i32;
}`},
		{"the stage changes the element type", `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: i64[] = xs.map((x: f64): i64 => (x * 2.0) as i64);
  return ys[0] as i32;
}`},
	} {
		if _, n := scaleProgram(t, tc.body); n != 0 {
			t.Errorf("rewrote %d sites where %s; want none", n, tc.why)
		}
	}
}

// The battery runs this pass AFTER R7, so R7 never loses a site to it. R7
// allocates NOTHING, and that is a contract fip/E068 checks; a kernel putting
// a fresh buffer back would break a claim a program is allowed to make.
//
// There is no contention TODAY, because R7's first slice declines f64 —
// `element-width-unsupported`, its 8-byte rule being an integer one. This
// pins that, so the day R7 widens to f64 the ordering is re-read rather than
// silently depended on.
func TestR7StillDeclinesF64SoTheOrderIsUntested(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function twice(own xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 2.0); }
function main(): i32 {
  var ys: f64[] = twice([1.0, 2.0]);
  return (ys[0] + ys[1]) as i32;
}`)
	if n := ir.MapOwnedArrayInPlace(p, 8); n != 0 {
		t.Fatalf("R7 now takes %d f64 sites. It used to take none, so the scale kernel's "+
			"place after it in the battery was never exercised — check that ordering before "+
			"loosening this test", n)
	}
	if n := ir.ScaleF64Maps(p); n != 1 {
		t.Errorf("the scale kernel took %d of the f64 site R7 declined, want 1", n)
	}
}

// The off switch, for the reason fusion and R7 have one.
func TestScaleKernelOffSwitch(t *testing.T) {
	t.Setenv("FERN_NO_SCALE_KERNEL", "1")
	if _, n := scaleProgram(t, `function main(): i32 {
  var xs: f64[] = [1.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.0);
  return ys[0] as i32;
}`); n != 0 {
		t.Errorf("rewrote %d sites with the kernel switched off, want none", n)
	}
}

// The shapes internal/e2e/array_scale_kernel_test.go runs, pinned here as
// rewrites. That suite compares each against a hand-written loop and would
// pass just as well if nothing had been rewritten, so what makes it a test of
// the kernel rather than of the scalar loop is this count.
func TestScaleKernelTakesEveryShapeTheE2ECompares(t *testing.T) {
	_, n := scaleProgram(t, `function scale2(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 2.0); }
function scale_neg(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * -1.5); }
function scale_zero(xs: f64[]): f64[] { return xs.map((x: f64): f64 => x * 0.0); }
function half(x: f64): f64 { return x * 0.5; }
function scale_named(xs: f64[]): f64[] { return xs.map(half); }
function main(): i32 {
  var xs: f64[] = [1.0, 2.0];
  return (scale2(xs)[0] + scale_neg(xs)[0] + scale_zero(xs)[0] + scale_named(xs)[0]) as i32;
}`)
	if n != 4 {
		t.Errorf("rewrote %d of the four shapes the e2e suite compares, want 4 — "+
			"a shape that stopped rewriting makes that suite compare the scalar loop with itself", n)
	}
}

// The receiver is whatever was pushed before the replaced range opened, and
// the range never touches it — so a receiver that is another call's result,
// held on the stack rather than in a slot, works without the pass knowing
// anything about it. Two chained scales also prove the rewrite leaves a stack
// the next one can be planned against.
func TestScaleKernelTakesAChainedReceiver(t *testing.T) {
	p, n := scaleProgram(t, `function main(): i32 {
  var xs: f64[] = [1.0, 2.0];
  var ys: f64[] = xs.map((x: f64): f64 => x * 2.0).map((x: f64): f64 => x * 3.0);
  return ys[0] as i32;
}`)
	if n != 2 {
		t.Fatalf("rewrote %d of the two chained scales, want 2", n)
	}
	var kernels int
	for _, op := range mainOps(t, p) {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_scale_f64" {
			kernels++
		}
	}
	if kernels != 2 {
		t.Errorf("%d kernel calls after two rewrites, want 2", kernels)
	}
}
