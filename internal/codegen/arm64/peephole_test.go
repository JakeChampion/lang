package arm64

// Tests for the streaming output peephole (generator.put / peepholeTail) —
// the arm64 mirror of the x86-64 backend's peephole.
//
//   P1  a push immediately followed by the matching pop:
//         str x0, [sp, #-N]! / ldr DST, [sp], #N  =>  mov DST, x0
//   P2  an unconditional `b L` immediately followed by the label `L:`.
//
// It must NOT collapse a non-adjacent push/pop (a genuinely live stack slot,
// left for the register allocator). These assert the collapse happens, that
// the conditional/`bl` branches are untouched, and the opt-out — without an
// assembler or qemu.

import (
	"strings"
	"testing"
)

func instrCount(asm string) int {
	n := 0
	for _, l := range strings.Split(asm, "\n") {
		if len(l) > 0 && l[0] == '\t' {
			n++
		}
	}
	return n
}

// countAdjacentPushPop returns how many `str x0, [sp, #-16]!` lines are
// immediately followed by a `ldr xN, [sp], #16` pop.
func countAdjacentPushPop(asm string) int {
	lines := strings.Split(asm, "\n")
	n := 0
	for i := 0; i+1 < len(lines); i++ {
		cur := strings.TrimSpace(lines[i])
		next := strings.TrimSpace(lines[i+1])
		if cur == "str x0, [sp, #-16]!" &&
			strings.HasPrefix(next, "ldr ") && strings.HasSuffix(next, ", [sp], #16") {
			n++
		}
	}
	return n
}

func hasBranchToNextLabel(asm string) bool {
	lines := strings.Split(asm, "\n")
	for i := 0; i+1 < len(lines); i++ {
		cur := strings.TrimSpace(lines[i])
		next := strings.TrimSpace(lines[i+1])
		if strings.HasPrefix(cur, "b ") && next == strings.TrimPrefix(cur, "b ")+":" {
			return true
		}
	}
	return false
}

// g(a,b,c)=a+b+c pushes/pops operands adjacently — five str/ldr pairs with the
// peephole off — exercising P1. (P2 is asserted as "no branch-to-next-label
// survives", true on both programs.)
const peepProg = `
function g(a: i32, b: i32, c: i32): i32 { return a + b + c; }
function main(): i32 { return g(1, 2, 3); }`

func TestPeepholeCollapsesAdjacentPushPop(t *testing.T) {
	off := compile(t, peepProg, Options{NoPeephole: true})
	on := compile(t, peepProg, Options{})

	if countAdjacentPushPop(off) == 0 {
		t.Fatal("precondition: un-peepholed asm has no adjacent push/pop to collapse")
	}
	if got := countAdjacentPushPop(on); got != 0 {
		t.Errorf("P1: %d adjacent push/pop pairs survived the peephole", got)
	}
	if hasBranchToNextLabel(on) {
		t.Error("P2: branch-to-next-label survived the peephole")
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d",
			instrCount(off), instrCount(on))
	}
}

// Only the bare unconditional `b L` is a safe fall-through to remove.
// Conditional branches (`b.cond`, `cbz`, `cbnz`, `tbz`, `tbnz`) and `bl`
// calls must be preserved. Assert the peephole does not change the count of
// any non-`b ` branch mnemonic between the off and on emissions.
func TestPeepholePreservesConditionalAndCallBranches(t *testing.T) {
	src := `
function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function main(): i32 { return fib(10); }`
	off := compile(t, src, Options{NoPeephole: true})
	on := compile(t, src, Options{})

	for _, mnem := range []string{"bl ", "b.", "cbz ", "cbnz ", "tbz ", "tbnz "} {
		if c := strings.Count(on, "\t"+mnem); c != strings.Count(off, "\t"+mnem) {
			t.Errorf("peephole changed count of %q branch: off=%d on=%d",
				strings.TrimSpace(mnem), strings.Count(off, "\t"+mnem), c)
		}
	}
	// Sanity: the program actually contains conditional branches and a call,
	// so the preservation assertion is meaningful.
	if strings.Count(off, "\tbl ") == 0 {
		t.Fatal("precondition: fib should emit bl calls")
	}
}

func TestPeepholeOptOutEmitsUncollapsed(t *testing.T) {
	off := compile(t, peepProg, Options{NoPeephole: true})
	if countAdjacentPushPop(off) == 0 {
		t.Error("NoPeephole should leave the adjacent push/pop pairs in place")
	}
}

// deadPushProg has statement-position expressions whose values nobody reads:
// each assignment leaves its result on the operand stack and the statement end
// discards it. That is P3's shape.
const deadPushProg = `
function main(): i32 {
  var t: i32 = 0;
  var i: i32 = 0;
  while (i < 4) { t = t + i; i = i + 1; }
  return t;
}`

// hasDeadPush reports whether a push is immediately followed by the matching
// stack restore, with nothing having read the slot.
func hasDeadPush(asm string) bool {
	lines := strings.Split(asm, "\n")
	for i := 0; i+1 < len(lines); i++ {
		cur := strings.TrimSpace(lines[i])
		next := strings.TrimSpace(lines[i+1])
		if !strings.HasPrefix(cur, "str x0, [sp, #-") || !strings.HasSuffix(cur, "]!") {
			continue
		}
		n := strings.TrimSuffix(strings.TrimPrefix(cur, "str x0, [sp, #-"), "]!")
		if next == "add sp, sp, #"+n {
			return true
		}
	}
	return false
}

func TestPeepholeRemovesDeadPush(t *testing.T) {
	off := compile(t, deadPushProg, Options{NoPeephole: true})
	on := compile(t, deadPushProg, Options{})

	if !hasDeadPush(off) {
		t.Fatal("precondition: un-peepholed asm has no dead push to remove")
	}
	if hasDeadPush(on) {
		t.Error("P3: a push whose slot is freed unread survived the peephole")
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d",
			instrCount(off), instrCount(on))
	}
}

// P3 must not touch a push whose slot IS read before it is freed — that is a
// live operand-stack slot, and removing it would drop the value.
func TestPeepholePreservesReadBeforeFree(t *testing.T) {
	on := compile(t, peepProg, Options{})
	lines := strings.Split(on, "\n")
	sawLive := false
	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "str x0, [sp, #-") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			cur := strings.TrimSpace(lines[j])
			if strings.HasPrefix(cur, "add sp, sp, #") {
				break
			}
			if strings.Contains(cur, "[sp], #") {
				sawLive = true
				break
			}
		}
	}
	if !sawLive {
		t.Error("no live push survived: P3 removed a slot that was read before release")
	}
}
