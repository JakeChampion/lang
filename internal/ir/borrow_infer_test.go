package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// Borrow inference (BorrowInferEnabled): a parameter the escape analysis proves
// non-escaping is kept BORROWED — the caller skips the retain inc and the callee
// skips the exit dec. These pin, at the IR layer, that a pure reader (`sum`,
// non-escaping `l`) loses BOTH its caller-side incs (the two aliased call sites)
// and its callee-side reclamation, while the differential gate
// (Test{X86_64,Arm64,WASM}BorrowInferMatchesOwned) proves it stays
// byte-identical end to end.

func rcOpCount(ip *ir.Program, fn, needle string) int {
	f := funcByName(ip, fn)
	if f == nil {
		return -1
	}
	n := 0
	for _, op := range f.Ops {
		if (op.Kind == ir.OpCallDirect || op.Kind == ir.OpRcInc || op.Kind == ir.OpRcDec || op.Kind == ir.OpRcIsUnique) && strings.Contains(op.Str, needle) {
			n++
		}
	}
	return n
}

const borrowInferSrc = `enum L{C(i32,L),N}
function sum(l:L):i32{match(l){C(h,t)=>{return h+sum(t);},N=>{return 0;}}}
function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));}
function f():i32{var x:L=build(3);return sum(x)+sum(x);}
function main():i32{return 0;}`

// Caller side: under the owned model the first `sum(x)` incs x (x is read
// again), and the second — x's last use — MOVES it (computeOwnedArgMoves), so
// exactly one inc; under borrow inference sum borrows, so even that vanishes.
func TestBorrowInferElidesReaderCallerInc(t *testing.T) {
	prev := ast.BorrowInferEnabled
	defer func() { ast.BorrowInferEnabled = prev }()

	ast.BorrowInferEnabled = false
	off := rcOpCount(lowerForTest(t, borrowInferSrc), "f", "rc_inc")
	ast.BorrowInferEnabled = true
	on := rcOpCount(lowerForTest(t, borrowInferSrc), "f", "rc_inc")

	if off != 1 {
		t.Fatalf("expected the owned model to inc the aliased reader arg at the first call and move it at the last, got %d incs", off)
	}
	if on != 0 {
		t.Errorf("borrow inference should elide the reader-arg incs in f, got %d (off=%d)", on, off)
	}
}

// Callee side: under the owned model sum reclaims its argument (dec / box_free at
// exit); borrowed, it touches no rc at all.
func TestBorrowInferElidesReaderCalleeDrop(t *testing.T) {
	prev := ast.BorrowInferEnabled
	defer func() { ast.BorrowInferEnabled = prev }()

	ast.BorrowInferEnabled = false
	offDec := rcOpCount(lowerForTest(t, borrowInferSrc), "sum", "rc_dec")
	offFree := rcOpCount(lowerForTest(t, borrowInferSrc), "sum", "box_free")
	ast.BorrowInferEnabled = true
	onDec := rcOpCount(lowerForTest(t, borrowInferSrc), "sum", "rc_dec")
	onFree := rcOpCount(lowerForTest(t, borrowInferSrc), "sum", "box_free")

	if offDec+offFree == 0 {
		t.Fatalf("expected the owned model to reclaim sum's argument (dec/free), got dec=%d free=%d", offDec, offFree)
	}
	if onDec != 0 || onFree != 0 {
		t.Errorf("borrow inference should leave sum's borrowed arg untouched, got dec=%d free=%d", onDec, onFree)
	}
}

// A param that ESCAPES (returned) must stay owned — borrow inference must not
// touch it. `id` returns its parameter, so `p` escapes and keeps its reclamation
// contract; the differential gate would catch a mis-borrow, this pins intent.
// x is read again after `id(x)`, so the argument is retained rather than
// moved; `dying` is the same call at x's last use, where the owned position
// takes the reference itself (computeOwnedArgMoves) and no inc is emitted.
func TestBorrowInferKeepsEscapingParamOwned(t *testing.T) {
	prev := ast.BorrowInferEnabled
	defer func() { ast.BorrowInferEnabled = prev }()

	const src = `enum L{C(i32,L),N}
function id(p:L):L{return p;}
function len(l:L):i32{match(l){C(h,t)=>{return 1+len(t);},N=>{return 0;}}}
function build(n:i32):L{if(n==0){return N;}return C(n,build(n-1));}
function g():i32{var x:L=build(3);return len(id(x))+len(x);}
function dying():i32{var x:L=build(3);return len(id(x));}
function main():i32{return 0;}`

	ast.BorrowInferEnabled = false
	off := rcOpCount(lowerForTest(t, src), "g", "rc_inc")
	ast.BorrowInferEnabled = true
	on := rcOpCount(lowerForTest(t, src), "g", "rc_inc")
	moved := rcOpCount(lowerForTest(t, src), "dying", "rc_inc")

	// `id` escapes p (returns it) so its arg stays owned (inc'd) under both
	// models; `len` is a borrowable reader. The escaping-arg inc must survive.
	if on < 1 {
		t.Errorf("escaping-param call must keep its retain inc under borrow inference, got %d (off=%d)", on, off)
	}
	if moved != 0 {
		t.Errorf("an escaping-position argument at its last use must be moved, not retained; got %d incs", moved)
	}
}
