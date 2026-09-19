package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Folding the env fetch at a zero-capture closure's call sites (#9731's
// residual).
//
// `OpConstFunc` materialises a `{fn_ptr, env_ptr}` pair whose env_ptr is 0,
// and `Defunctionalise` nonetheless emits four ops at every call site to read
// that constant back out of memory. Inside a loop that is three memory touches
// per call per element; on the fused `map.map.reduce` benchmark it was the
// whole of what still separated it from a hand-written loop CALLING the same
// element functions.
//
// The negatives matter more than the positive here. A capturing closure's env
// is a real pointer, and folding it to 0 would hand every call an env of NULL
// — the captured values silently read as garbage rather than failing to
// compile. So the pass keys on `OpConstFunc`, which by construction has no
// env, and anything else keeps the fetch.

// envFetches counts the `load slot; +off; add; load` sequences feeding a
// direct closure call in `fn`.
func envFetches(p *ir.Program, fn string, pairEnvOffset int32) int {
	n := 0
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for i := 0; i+4 < len(f.Ops); i++ {
			if f.Ops[i].Kind == ir.OpLoadLocal &&
				f.Ops[i+1].Kind == ir.OpConstI32 && f.Ops[i+1].I32 == pairEnvOffset &&
				f.Ops[i+2].Kind == ir.OpAdd &&
				f.Ops[i+3].Kind == ir.OpLoad &&
				f.Ops[i+4].Kind == ir.OpCallClosureDirect {
				n++
			}
		}
	}
	return n
}

// closureCalls counts direct closure calls in `fn`, so a test can tell "the
// fetch went away" from "the call went away".
func closureCalls(p *ir.Program, fn string) int {
	n := 0
	for _, f := range p.Funcs {
		if f.Name != fn {
			continue
		}
		for _, op := range f.Ops {
			if op.Kind == ir.OpCallClosureDirect {
				n++
			}
		}
	}
	return n
}

const zeroEnvSrc = `import "std/array";
function k(): i64 { return 7 as i64; }
function f(x: i64): i64 { return x + k(); }
function g(x: i64): i64 { return x * k(); }
function h(a: i64, b: i64): i64 { return a + b + k(); }
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => f(x))
           .map((x: i64): i64 => g(x))
           .fold(0 as i64, (a: i64, b: i64): i64 => h(a, b));
}
function main(): i32 { return run([1 as i64]) as i32; }`

func TestZeroCaptureEnvFetchIsFolded(t *testing.T) {
	p := lowerPipelineSrc(t, zeroEnvSrc)
	ir.OptimizeProgram(p, 8)
	// The calls survive — these element functions are not leaves, so Inline
	// leaves them alone. Without this the next assertion would pass for the
	// wrong reason.
	if got := closureCalls(p, "run"); got != 3 {
		t.Fatalf("run makes %d direct closure calls, want 3 — the fetch assertion below "+
			"would otherwise be vacuous", got)
	}
	if got := envFetches(p, "run", 8); got != 0 {
		t.Errorf("run still reads the env back from memory at %d call sites: these closures "+
			"capture nothing, so their env is the constant 0", got)
	}
}

// The rejection half — that a slot written by anything but a plain function
// value keeps its fetch — is exercised directly on hand-built IR in
// zero_env_internal_test.go. No source program reaches that shape: a
// capturing closure is an OpMakeClosure with captures, which
// InlineZeroCaptureClosures leaves alone and ElideClosurePair usually
// rewrites before this pass runs. A test written through the compiler
// therefore skips rather than asserts, which is worse than no test.
