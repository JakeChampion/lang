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
@noinline function g(a: i32, b: i32, c: i32): i32 { return a + b + c; }
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

// roundTripProg has an expression whose left operand is pushed, overwritten in
// x0 by the load of the right operand, then popped into another register to be
// combined — the shape P4 rewrites. The recursion is what keeps it: inlined
// into main the whole body folds to a constant and no operand reaches the
// stack at all.
const roundTripProg = `function f(a: i64, b: i64): i64 {
    if (a > 100i64) { return f(a - 1i64, b); }
    return a + b;
}
function main(): i32 { return f(3i64, 4i64) as i32; }`

// countRoundTrips returns how many `str x0, [sp, #-16]!` lines are separated
// from their matching pop by exactly one instruction — what P4 removes.
func countRoundTrips(asm string) int {
	lines := strings.Split(asm, "\n")
	n := 0
	for i := 0; i+2 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "str x0, [sp, #-16]!" {
			continue
		}
		mid := lines[i+1]
		if len(mid) == 0 || mid[0] != '\t' || strings.HasPrefix(mid, "\t.") {
			continue
		}
		pop := strings.TrimSpace(lines[i+2])
		if strings.HasPrefix(pop, "ldr ") && strings.HasSuffix(pop, ", [sp], #16") {
			n++
		}
	}
	return n
}

func TestPeepholeCollapsesRoundTrip(t *testing.T) {
	on := compile(t, roundTripProg, Options{})
	off := compile(t, roundTripProg, Options{NoPeephole: true})

	if before := countRoundTrips(off); before == 0 {
		t.Fatalf("fixture emits no push/op/pop round trip, so this test proves nothing:\n%s", off)
	}
	if after := countRoundTrips(on); after != 0 {
		t.Errorf("P4 left %d push/op/pop round trips in place; asm:\n%s", after, on)
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("P4 did not shorten the output: %d instructions with the peephole, %d without",
			instrCount(on), instrCount(off))
	}
}

func TestRoundTripSafeMid(t *testing.T) {
	cases := []struct {
		name string
		line string
		dst  string
		want bool
	}{
		{"frame load into x0", "\tldur x0, [x29, #-16]", "x1", true},
		{"immediate into x0", "\tmovz x0, #7", "x3", true},
		{"w-width write to x0", "\tmov w0, w2", "x1", true},
		// The mov P4 inserts precedes this line, so naming the pop destination
		// in either width would read the moved value instead of the old one.
		{"reads the pop destination", "\tadd x0, x1, x0", "x1", false},
		{"reads it in w form", "\tmov w0, w1", "x1", false},
		// x10 is not x1: the register match is whole-token.
		{"similar register number", "\tadd x0, x10, x0", "x1", true},
		{"touches sp", "\tldr x0, [sp, #8]", "x1", false},
		// Not whitelisted: a call clobbers x0 and the caller-saved registers, a
		// store has a memory effect, a branch can leave the region.
		{"call", "\tbl __fern_alloc", "x1", false},
		{"store", "\tstr x0, [x1]", "x1", false},
		{"branch", "\tb .L1", "x1", false},
		{"label", ".L1:", "x1", false},
		// Writing some other register is fine on its own line — the run as a
		// whole is what has to free x0, which roundTripSafeMids checks.
		{"writes another register", "\tldur x2, [x29, #-16]", "x1", true},
		// Writing the pop destination is not: the moved value has to survive
		// to where the pop used to be.
		{"writes the pop destination", "\tldur x1, [x29, #-16]", "x1", false},
	}
	for _, c := range cases {
		if got := roundTripSafeMid(c.line, c.dst); got != c.want {
			t.Errorf("%s: roundTripSafeMid(%q, %q) = %v, want %v", c.name, c.line, c.dst, got, c.want)
		}
	}
}

func TestRoundTripSafeMids(t *testing.T) {
	cases := []struct {
		name string
		mids []string
		want bool
	}{
		{"single x0 write", []string{"\tldur x0, [x29, #-16]"}, true},
		{"x0 written later in the run", []string{"\tldur x2, [x29, #-8]", "\tmov x0, x2"}, true},
		// Nothing in the run frees x0, so the push was not this pattern.
		{"never writes x0", []string{"\tldur x2, [x29, #-8]", "\tadd x2, x2, #1"}, false},
		{"empty run", nil, false},
		// One unsafe line disqualifies the whole run.
		{"call in the run", []string{"\tldur x0, [x29, #-8]", "\tbl f"}, false},
		{"names the destination", []string{"\tldur x0, [x29, #-8]", "\tadd x2, x1, x2"}, false},
	}
	for _, c := range cases {
		if got := roundTripSafeMids(c.mids, "x1"); got != c.want {
			t.Errorf("%s: roundTripSafeMids(%q) = %v, want %v", c.name, c.mids, got, c.want)
		}
	}
}

func TestMentionsReg(t *testing.T) {
	cases := []struct {
		s, reg string
		want   bool
	}{
		{"x1, x2", "x1", true},
		{"x10, x2", "x1", false},
		{"[sp, #8]", "sp", true},
		{"[x29, #-8]", "sp", false},
		{"#0x1f", "x1", false},
		{"x2, x1", "x1", true},
		{"w1, w2", "w1", true},
		{"", "x1", false},
	}
	for _, c := range cases {
		if got := mentionsReg(c.s, c.reg); got != c.want {
			t.Errorf("mentionsReg(%q, %q) = %v, want %v", c.s, c.reg, got, c.want)
		}
	}
}
