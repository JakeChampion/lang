package x86_64

// Which operand depth is free at a call is the prologue's choice, and this
// backend used to make the wrong one.
//
// System V wants rsp 16-aligned at every `call`; an 8-byte operand slot flips
// that, so exactly one of the two depths needs no `sub rsp, 8` / `add rsp, 8`
// bracket. A 16-byte-multiple frame picks depth 0 — but a call in the middle
// of an expression happens with an odd number of live slots, and that is
// nearly every call in real code. Biasing the frame by 8 moves the free depth
// to where the calls are.
//
// The alignment half of the contract is in stack_align_test.go, which
// re-derives rsp from the emitted text; these tests are about the bias being
// chosen, and being free.

import (
	"strings"
	"testing"
)

// A call reached with one live operand slot is the common shape — the left
// operand evaluated, the callee's result about to be combined with it.
const parityProg = `
@noinline function f(x: i32): i32 { return x + 1; }
function main(): i32 {
  var acc: i32 = 0;
  var i: i32 = 0;
  while (i < 4) { acc = acc + f(i); i = i + 1; }
  return acc;
}`

func TestCallAtOddOperandDepthNeedsNoPad(t *testing.T) {
	asm := compile(t, parityProg)
	body, ok := fnBodyOf(asm, "main")
	if !ok {
		t.Fatal("main not found in emitted asm")
	}
	if strings.Contains(body, "sub rsp, 8") || strings.Contains(body, "add rsp, 8") {
		t.Errorf("a call at odd operand depth still pays an alignment bracket:\n%s", body)
	}
	// And it is still correct: every call sees a 16-aligned rsp.
	if problems := checkStackAlignment(asm); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// The bias has to ride inside the `sub rsp, N` the frame already needs. If it
// ever becomes its own instruction the trade stops paying, because a function
// with one padded call would break even and one with none would lose.
func TestFrameBiasCostsNoInstruction(t *testing.T) {
	asm := compile(t, parityProg)
	body, ok := fnBodyOf(asm, "main")
	if !ok {
		t.Fatal("main not found in emitted asm")
	}
	n := strings.Count(body, "\tsub rsp,")
	if n != 1 {
		t.Errorf("prologue should adjust rsp exactly once, got %d:\n%s", n, body)
	}
}

// A frameless function has no `sub rsp` to hide the bias in, so it keeps the
// canonical parity rather than spending a whole instruction on a pad it may
// never reach. Frameless means no locals AND no parameters — a parameter is
// spilled to a home slot on entry, which is a frame of its own.
// TestNoPadWhereAlignmentAlreadyHolds is the other side of this.
func TestFramelessFunctionIsNotBiased(t *testing.T) {
	asm := compile(t, `
@noinline function answer(): i32 { return 42; }
function main(): i32 { return answer(); }`)
	body, ok := fnBodyOf(asm, "answer")
	if !ok {
		t.Fatal("answer not found in emitted asm")
	}
	if strings.Contains(body, "sub rsp") {
		t.Errorf("frameless function grew a frame for the bias:\n%s", body)
	}
}

// Alignment must survive the shapes that move rsp for their own reasons —
// stack arguments beyond the six register slots, and the overflow area that
// doubles as the pad.
func TestBiasHoldsWithStackArguments(t *testing.T) {
	asm := compile(t, `
@noinline function nine(a: i32, b: i32, c: i32, d: i32, e: i32,
                        f: i32, g: i32, h: i32, i: i32): i32 {
  return a + b + c + d + e + f + g + h + i;
}
function main(): i32 {
  var t: i32 = 0;
  var k: i32 = 0;
  while (k < 3) { t = t + nine(k, 1, 2, 3, 4, 5, 6, 7, 8); k = k + 1; }
  return t;
}`)
	if problems := checkStackAlignment(asm); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}
