package e2e

import (
	"testing"
)

var obdStructTupleCases = map[string]string{
	"struct-read-fresh":      `struct P{x:i32,y:i32} function sx(p:P):i32{return p.x+p.y;} function main():i32{if(sx(P{x:3,y:4})!=7){return 100;}return __rc_underflow_count();}`,
	"struct-read-twice":      `struct P{x:i32,y:i32} function sx(p:P):i32{return p.x+p.y;} function main():i32{let q:P=P{x:3,y:4};if(sx(q)+sx(q)!=14){return 100;}return __rc_underflow_count();}`,
	"struct-reuse-overwrite": `struct P{x:i32,y:i32} function bump(p:P):P{p=P{x:p.x+1,y:p.y};return p;} function main():i32{let q:P=bump(bump(P{x:0,y:9}));if(q.x!=2){return 100;}if(q.y!=9){return 101;}return __rc_underflow_count();}`,
	"struct-nested":          `struct Inner{v:i32} struct Outer{a:Inner,b:i32} function f(o:Outer):i32{return o.a.v+o.b;} function main():i32{if(f(Outer{a:Inner{v:5},b:6})!=11){return 100;}return __rc_underflow_count();}`,
	"struct-passthru":        `struct P{x:i32} function id(p:P):P{return p;} function main():i32{if(id(P{x:7}).x!=7){return 100;}return __rc_underflow_count();}`,
	"struct-enum-field":      `enum E{A(i32),B} struct W{e:E,n:i32} function f(w:W):i32{match(w.e){A(x)=>{return x+w.n;},B=>{return w.n;}}} function main():i32{if(f(W{e:A(5),n:6})!=11){return 100;}return __rc_underflow_count();}`,
	"tuple-read":             `function f(t:(i32,i32)):i32{return t.0+t.1;} function main():i32{if(f((3,4))!=7){return 100;}return __rc_underflow_count();}`,
	"tuple-twice":            `function f(t:(i32,i32)):i32{return t.0+t.1;} function main():i32{let p:(i32,i32)=(3,4);if(f(p)+f(p)!=14){return 100;}return __rc_underflow_count();}`,
	"struct-tuple-field":     `struct S{p:(i32,i32),n:i32} function f(s:S):i32{return s.p.0+s.p.1+s.n;} function main():i32{if(f(S{p:(3,4),n:5})!=12){return 100;}return __rc_underflow_count();}`,
}

func TestX86_64OwnedByDefaultStructTupleSound(t *testing.T) {
	for name, src := range obdStructTupleCases {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0 (100/101=value, >0=over-release)", name, code)
		}
	}
}

func TestArm64OwnedByDefaultStructTupleSound(t *testing.T) {
	for name, src := range obdStructTupleCases {
		if _, code := compileAndRunArm64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0", name, code)
		}
	}
}

func TestWASMOwnedByDefaultStructTupleSound(t *testing.T) {
	for name, src := range obdStructTupleCases {
		if got := runWasm(t, src); got != 0 {
			t.Errorf("%s: got %d, want 0", name, got)
		}
	}
}

func obdStructBoundedSrc(n string) string {
	return `struct P{x:i32,y:i32}
function sx(p:P):i32{return p.x+p.y;}
function main():i32{
    let before:i32=(__heap_bump_bytes() as i32);
    let i:i32=0; let s:i32=0;
    while(i<` + n + `){ s=s+sx(P{x:i,y:1}); i=i+1; }
    if(s<0){return 1;}
    return (__heap_bump_bytes() as i32)-before;
}`
}

func TestX86_64OwnedByDefaultStructBounded(t *testing.T) {
	small := mustRunX86_64FreeOn(t, obdStructBoundedSrc("50"))
	large := mustRunX86_64FreeOn(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestArm64OwnedByDefaultStructBounded(t *testing.T) {
	small := mustRunArm64FreeOn(t, obdStructBoundedSrc("50"))
	large := mustRunArm64FreeOn(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}

func TestWASMOwnedByDefaultStructBounded(t *testing.T) {
	small := runWasm(t, obdStructBoundedSrc("50"))
	large := runWasm(t, obdStructBoundedSrc("5000"))
	if small != large {
		t.Errorf("owned struct arg should be reclaimed (bounded): N=50 -> %d, N=5000 -> %d", small, large)
	}
}
