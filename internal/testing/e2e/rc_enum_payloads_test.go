package e2e

import "testing"

// FBIP shapes must be rc-balanced: value-correct +
// __rc_underflow_count()==0. Covers the payload-source cases (fresh build,
// aliased borrowed, moved own-param, iterative self-build) and the consuming /
// pipeline / tree traversals that enum payload counting has to keep balanced.
func TestX86_64EnumRcPayloadsSound(t *testing.T) {
	cases := map[string]string{
		"alias-borrow": `enum L{C(i32,L),N} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function f(t:L):i32{let e:L=C(0,t);return len(t)+len(e);} function main():i32{let b:L=build(3);if(f(b)!=7){return 100;}return __rc_underflow_count();}`,
		"moved-own":    `enum L{C(i32,L),N} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function wrap(own t:L):L{let e:L=C(0,t);return e;} function main():i32{if(len(wrap(build(3)))!=4){return 100;}return __rc_underflow_count();}`,
		"iter-build":   `enum L{C(i32,L),N} function eat(own xs:L):i32{match(xs){C(h,t)=>{return h+eat(t);},N=>{return 0;}}} function ib(n:i32):L{let acc:L=N;let i:i32=0;while(i<n){acc=C(1,acc);i=i+1;}return acc;} function main():i32{if(eat(ib(8))!=8){return 100;}return __rc_underflow_count();}`,
		"consuming":    `enum L{C(i32,L),N} function (own xs:L) inc():L{match(xs){C(h,t)=>{return C(h+1,t.inc());},N=>{return N;}}} function sum(l:L):i32{match(l){C(h,t)=>{return h+sum(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{if(sum(build(5).inc())!=20){return 100;}return __rc_underflow_count();}`,
		// #10700: a pointer payload aliased out of a Result box by the arm
		// outlives the box, and is read twice after it.
		"alias-from-result-box": `struct M{xs:string[]} function mk():Result[M,i32]{let h:M=M{xs:[]};h=M{xs:h.xs.append("a")};return Ok(h);} function main():i32{let m:M=M{xs:[]};match(mk()){Ok(h)=>{m=h;},Err(s)=>{return s;}} if(m.xs.len()!=1){return 100;} if(m.xs.len()!=1){return 101;} return __rc_underflow_count();}`,
		"tree":                  `enum T{Leaf(i32),Node(T,T)} function s(t:T):i32{match(t){Leaf(x)=>{return x;},Node(l,r)=>{return s(l)+s(r);}}} function mk(d:i32):T{if(d==0){return Leaf(1);}return Node(mk(d-1),mk(d-1));} function main():i32{if(s(mk(4))!=16){return 100;}return __rc_underflow_count();}`,
	}
	for name, src := range cases {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0 (value or over-release)", name, code)
		}
	}
}
