package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Slice 1b differential gate (docs/OWNERSHIP-INFERENCE-PLAN.md): with free ON,
// the EnumRcPayloads model (enum construction rc-counts its pointer payloads,
// unblocking FBIP enum precise drops) must produce byte-identical OUTPUT whether
// it's on or the default move model is used — rc is semantically invisible, so
// any divergence is an over-release or a corrupted reclaim surfaced by a real
// program. Same corpus + helpers as the free/reuse gates; only EnumRcPayloads
// flips (free stays on for both runs).

// enumRcPayloadsKnownDivergent lists fixtures whose SHAPE the move model cannot
// express, so the on==off premise does not apply to them. They still run under
// the production model (EnumRcPayloads is on by default) via TestFernFixtures on
// all four backends — only this differential skips them.
//
// The divergence is in the model, not in a backend, so one list covers all three
// legs.
var enumRcPayloadsKnownDivergent = map[string]bool{
	// #6720: a consuming walk decides per level, from the cell's own refcount,
	// whether it may take the box. Under the move model an ALIASED payload is
	// not counted at all — `Cons(7, shared)` leaves the caller's head at 1 —
	// so the walk calls a shared cell unique and reclaims a list `shared` is
	// still using. Counting that payload is the whole content of the fix, and
	// it is exactly what this model turns off; there is no lowering that makes
	// the case hold with it off. Crashes on the natives, traps on wasm.
	"own_consume_borrowed_tail": true,
	// #10700: the same class one shape over. A pointer payload bound by a
	// match arm out of a `Result` box and aliased into an outer local
	// (`Ok(h) => { req_headers = h; }` over std/http's
	// `__parse_header_bytes`, which answers `Result[HeaderMap, i32]`) is
	// freed with the box under the move model, which counts no alias, and
	// read afterwards: a use-after-free FERN_RC_FREE_DEBUG=1 names, a
	// segfault once the block is reused. The production model is
	// sanitizer-clean on the case, and "alias-from-result-box" in
	// TestX86_64EnumRcPayloadsSound pins the shape under it.
	"http_cookies": true,
	// The same class through std/json: `@derive(json.FromJson)` reads a
	// `string[]` field by taking each element's string out of its
	// `JsonString` box, and the move model frees it with the JSON tree
	// the decode walked. The wasm leg traps on the dead strings when the
	// `User` is dropped; the natives read them before the blocks are
	// reused, so no native case can observe the free. The fixture's own
	// wasm leg in TestFernFixtures is the pin: it runs clean under the
	// production model and traps (exit 134) with the model off.
	"derive_from_json_shapes": true,
}

func TestX86_64EnumRcPayloadsMatchesMove(t *testing.T) {
	forEachRunnableFixture(t, "x86_64", func(t *testing.T, f *fixtureSpec) {
		if enumRcPayloadsKnownDivergent[f.name] {
			t.Skip("shape the move model cannot express — #6720")
		}
		prev := ast.EnumRcPayloads
		defer func() { ast.EnumRcPayloads = prev }()
		ast.EnumRcPayloads = false
		outOff, exitOff := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		ast.EnumRcPayloads = true
		outOn, exitOn := runFixtureX86_64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("enum-rc-payloads-on diverged from move model:\n move=(exit %d) %q\n rc  =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestArm64EnumRcPayloadsMatchesMove(t *testing.T) {
	forEachRunnableFixture(t, "arm64", func(t *testing.T, f *fixtureSpec) {
		if enumRcPayloadsKnownDivergent[f.name] {
			t.Skip("shape the move model cannot express — #6720")
		}
		prev := ast.EnumRcPayloads
		defer func() { ast.EnumRcPayloads = prev }()
		ast.EnumRcPayloads = false
		outOff, exitOff := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		ast.EnumRcPayloads = true
		outOn, exitOn := runFixtureArm64FreeOn(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("enum-rc-payloads-on diverged from move model:\n move=(exit %d) %q\n rc  =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

func TestWASMEnumRcPayloadsMatchesMove(t *testing.T) {
	forEachRunnableFixture(t, "wasm", func(t *testing.T, f *fixtureSpec) {
		if enumRcPayloadsKnownDivergent[f.name] {
			t.Skip("shape the move model cannot express — #6720")
		}
		prev := ast.RcFreeEnabled
		defer func() { ast.RcFreeEnabled = prev }()
		ast.RcFreeEnabled = true
		pe := ast.EnumRcPayloads
		defer func() { ast.EnumRcPayloads = pe }()
		ast.EnumRcPayloads = false
		outOff, exitOff := runFixtureWasm(t, f.mainPath, f.stdin)
		ast.EnumRcPayloads = true
		outOn, exitOn := runFixtureWasm(t, f.mainPath, f.stdin)
		if outOff != outOn || exitOff != exitOn {
			t.Errorf("enum-rc-payloads-on diverged from move model:\n move=(exit %d) %q\n rc  =(exit %d) %q", exitOff, outOff, exitOn, outOn)
		}
	})
}

// FBIP shapes must be rc-balanced under the EnumRcPayloads model: value-correct +
// __rc_underflow_count()==0. Covers the payload-source cases (fresh build,
// aliased borrowed, moved own-param, iterative self-build) and the consuming /
// pipeline / tree traversals that the model has to keep balanced.
func TestX86_64EnumRcPayloadsSound(t *testing.T) {
	prev := ast.EnumRcPayloads
	ast.EnumRcPayloads = true
	defer func() { ast.EnumRcPayloads = prev }()
	cases := map[string]string{
		"alias-borrow": `enum L{C(i32,L),N} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function f(t:L):i32{var e:L=C(0,t);return len(t)+len(e);} function main():i32{var b:L=build(3);if(f(b)!=7){return 100;}return __rc_underflow_count();}`,
		"moved-own":    `enum L{C(i32,L),N} function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function wrap(own t:L):L{var e:L=C(0,t);return e;} function main():i32{if(len(wrap(build(3)))!=4){return 100;}return __rc_underflow_count();}`,
		"iter-build":   `enum L{C(i32,L),N} function eat(own xs:L):i32{match(xs){C(h,t)=>{return h+eat(t);},N=>{return 0;}}} function ib(n:i32):L{var acc:L=N;var i:i32=0;while(i<n){acc=C(1,acc);i=i+1;}return acc;} function main():i32{if(eat(ib(8))!=8){return 100;}return __rc_underflow_count();}`,
		"consuming":    `enum L{C(i32,L),N} function (own xs:L) inc():L{match(xs){C(h,t)=>{return C(h+1,t.inc());},N=>{return N;}}} function sum(l:L):i32{match(l){C(h,t)=>{return h+sum(t);},N=>{return 0;}}} function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));} function main():i32{if(sum(build(5).inc())!=20){return 100;}return __rc_underflow_count();}`,
		// #10700: a pointer payload aliased out of a Result box by the arm
		// outlives the box, and is read twice after it.
		"alias-from-result-box": `struct M{xs:string[]} function mk():Result[M,i32]{var h:M=M{xs:[]};h=M{xs:h.xs.append("a")};return Ok(h);} function main():i32{var m:M=M{xs:[]};match(mk()){Ok(h)=>{m=h;},Err(s)=>{return s;}} if(m.xs.len()!=1){return 100;} if(m.xs.len()!=1){return 101;} return __rc_underflow_count();}`,
		"tree":                  `enum T{Leaf(i32),Node(T,T)} function s(t:T):i32{match(t){Leaf(x)=>{return x;},Node(l,r)=>{return s(l)+s(r);}}} function mk(d:i32):T{if(d==0){return Leaf(1);}return Node(mk(d-1),mk(d-1));} function main():i32{if(s(mk(4))!=16){return 100;}return __rc_underflow_count();}`,
	}
	for name, src := range cases {
		if _, code := compileAndRunX86_64FreeOn(t, src); code != 0 {
			t.Errorf("%s: got %d, want 0 (value or over-release)", name, code)
		}
	}
}
