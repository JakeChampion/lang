package e2e

import (
	"testing"
)

// Owned-by-default reader shapes must be rc-balanced (value-correct +
// __rc_underflow_count()==0): a reader reclaims a fresh argument (build → sum),
// a multi-use local (each call inc'd), a recursive tree reader, and a
// passthrough that returns its owned parameter. Consuming methods + iterative
// build (which lean on explicit `own`, untouched by this slice) stay sound too.
func TestX86_64OwnedByDefaultSound(t *testing.T) {
	cases := map[string]string{
		"read-fresh": `enum L{C(i32,L),N} function sum(l:L):i32{match(l){C(h,t)=>{return h+sum(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{if(sum(build(5))!=15){return 100;}return __rc_underflow_count();}`,
		"read-twice": `enum L{C(i32,L),N} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{let e:L=build(4);if(len(e)+len(e)!=8){return 100;}return __rc_underflow_count();}`,
		"tree":       `enum T{Leaf(i32),Node(T,T)} function s(t:T):i32{match(t){Leaf(x)=>{return x;},Node(l,r)=>{return s(l)+s(r);}}} function mk(d:i32):T{if(d==0){return Leaf(1);}return Node(mk(d-1),mk(d-1));} function main():i32{if(s(mk(4))!=16){return 100;}return __rc_underflow_count();}`,
		"passthru":   `enum L{C(i32,L),N} function id(l:L):L{return l;} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{if(len(id(build(3)))!=3){return 100;}return __rc_underflow_count();}`,
		"consuming":  `enum L{C(i32,L),N} function (own xs:L) inc():L{match(xs){C(h,t)=>{return C(h+1,t.inc());},N=>{return N;}}} function sum(l:L):i32{match(l){C(h,t)=>{return h+sum(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{if(sum(build(5).inc())!=20){return 100;}return __rc_underflow_count();}`,
	}
	for name, src := range cases {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0 (value or over-release)", name, code)
		}
	}
}
