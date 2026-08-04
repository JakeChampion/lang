package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Slice 2c — owned-by-default for STRUCTS and TUPLES. A struct/tuple parameter
// that is transitively string/array/slice/Map-free is now reclaimed by the
// callee (the caller retains an aliased arg with an inc, the callee dec's at
// exit), exactly like the enum case in 2a. Structs are mutated in place with no
// copy-on-write, so the retain inc never disturbs a mutation made through the
// parameter — these pin that key correctness point plus value + no over-release
// across reader / aliased / mutating / nested / passthrough / tuple shapes. The
// whole-corpus byte-identical proof is the differential gate
// (Test{X86_64,Arm64,WASM}OwnedByDefaultMatchesBorrow); this is the focused
// guard.

var obdStructTupleCases = map[string]string{
	// Reader reclaims a fresh argument (sole owner at the call).
	"struct-read-fresh": `struct P{x:i32,y:i32} function sx(p:P):i32{return p.x+p.y;} function main():i32{if(sx(P{x:3,y:4})!=7){return 100;}return __rc_underflow_count();}`,
	// Aliased arg used across two calls: each call retains with an inc, the
	// callee dec's, the caller keeps its reference; reclaimed at main's exit.
	"struct-read-twice": `struct P{x:i32,y:i32} function sx(p:P):i32{return p.x+p.y;} function main():i32{var q:P=P{x:3,y:4};if(sx(q)+sx(q)!=14){return 100;}return __rc_underflow_count();}`,
	// Whole-struct reassignment of an OWNED parameter (Fern structs are
	// immutable, so this is the mutation idiom): under owned-by-default a
	// fresh-temp arg is rc==1, so the `p = P{...}` self-overwrite reuses the box
	// in place (the FBIP win) — is_unique-gated, so it stays correct.
	"struct-reuse-overwrite": `struct P{x:i32,y:i32} function bump(p:P):P{p=P{x:p.x+1,y:p.y};return p;} function main():i32{var q:P=bump(bump(P{x:0,y:9}));if(q.x!=2){return 100;}if(q.y!=9){return 101;}return __rc_underflow_count();}`,
	// Nested struct payload: the deep drop walks one level into the inner box.
	"struct-nested": `struct Inner{v:i32} struct Outer{a:Inner,b:i32} function f(o:Outer):i32{return o.a.v+o.b;} function main():i32{if(f(Outer{a:Inner{v:5},b:6})!=11){return 100;}return __rc_underflow_count();}`,
	// Returning an owned parameter (passthrough): ownership flows back out.
	"struct-passthru": `struct P{x:i32} function id(p:P):P{return p;} function main():i32{if(id(P{x:7}).x!=7){return 100;}return __rc_underflow_count();}`,
	// Struct holding a nested enum (still string/array/Map-free).
	"struct-enum-field": `enum E{A(i32),B} struct W{e:E,n:i32} function f(w:W):i32{match(w.e){A(x)=>{return x+w.n;},B=>{return w.n;}}} function main():i32{if(f(W{e:A(5),n:6})!=11){return 100;}return __rc_underflow_count();}`,
	// Tuple parameter read.
	"tuple-read": `function f(t:(i32,i32)):i32{return t.0+t.1;} function main():i32{if(f((3,4))!=7){return 100;}return __rc_underflow_count();}`,
	// Tuple parameter, aliased across two reads.
	"tuple-twice": `function f(t:(i32,i32)):i32{return t.0+t.1;} function main():i32{var p:(i32,i32)=(3,4);if(f(p)+f(p)!=14){return 100;}return __rc_underflow_count();}`,
	// Nested tuple inside a struct.
	"struct-tuple-field": `struct S{p:(i32,i32),n:i32} function f(s:S):i32{return s.p.0+s.p.1+s.n;} function main():i32{if(f(S{p:(3,4),n:5})!=12){return 100;}return __rc_underflow_count();}`,
}

func TestX86_64OwnedByDefaultStructTupleSound(t *testing.T) {
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev }()
	for name, src := range obdStructTupleCases {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0 (100/101=value, >0=over-release)", name, code)
		}
	}
}

func TestArm64OwnedByDefaultStructTupleSound(t *testing.T) {
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev }()
	for name, src := range obdStructTupleCases {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0", name, code)
		}
	}
}

func TestWASMOwnedByDefaultStructTupleSound(t *testing.T) {
	prc := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev; ast.RcFreeEnabled = prc }()
	for name, src := range obdStructTupleCases {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("%s: got %d, want 0", name, got)
		}
	}
}

// A loop that hands a FRESH struct to a reader each iteration: under
// owned-by-default the reader reclaims it, so the bump high-water is BOUNDED
// (small == large) rather than growing with N. Pins no-leak for owned-by-default
// struct args.
func obdStructBoundedSrc(n string) string {
	return `struct P{x:i32,y:i32}
function sx(p:P):i32{return p.x+p.y;}
function main():i32{
    var before:i32=(__heap_bump_bytes() as i32);
    var i:i32=0; var s:i32=0;
    while(i<` + n + `){ s=s+sx(P{x:i,y:1}); i=i+1; }
    if(s<0){return 1;}
    return (__heap_bump_bytes() as i32)-before;
}`
}

func TestX86_64OwnedByDefaultStructBounded(t *testing.T) {
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev }()
	small := mustRunX86_64FreeOn(t, obdStructBoundedSrc("50"))
	large := mustRunX86_64FreeOn(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64OwnedByDefaultStructBounded(t *testing.T) {
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev }()
	small := mustRunArm64FreeOn(t, obdStructBoundedSrc("50"))
	large := mustRunArm64FreeOn(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMOwnedByDefaultStructBounded(t *testing.T) {
	prc := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	prev := ast.OwnedByDefault
	ast.OwnedByDefault = true
	defer func() { ast.OwnedByDefault = prev; ast.RcFreeEnabled = prc }()
	small := runWasm(t, obdStructBoundedSrc("50"))
	large := runWasm(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}
