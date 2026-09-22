package x86_64

// Tests for P12 (the zero-extending copy the index helpers leave behind) and
// for P8's reach through a compare and its branch — the two rules that take
// the per-byte cost out of a scan loop's `s[i]` read and its `&&` test.

import (
	"strings"
	"testing"
)

// runPeephole feeds lines through a generator's window and returns what the
// window holds afterwards.
func runPeephole(lines ...string) []string {
	g := &generator{}
	for _, l := range lines {
		g.put(l)
	}
	return append([]string(nil), g.peepWin...)
}

func sameLines(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestPeepholeFoldsZeroExtendingCopy(t *testing.T) {
	got := runPeephole("\tmov rcx, rax", "\tmov ecx, ecx")
	if !sameLines(got, "\tmov ecx, eax") {
		t.Errorf("got %q, want [mov ecx, eax]", got)
	}
	got = runPeephole("\tmov r8, rax", "\tmov r8d, r8d")
	if !sameLines(got, "\tmov r8d, eax") {
		t.Errorf("got %q, want [mov r8d, eax]", got)
	}
	// A self-move of a different register is not the pair.
	got = runPeephole("\tmov rcx, rax", "\tmov edx, edx")
	if !sameLines(got, "\tmov rcx, rax", "\tmov edx, edx") {
		t.Errorf("unrelated self-move was rewritten: %q", got)
	}
}

// The index helper's operand setup, as the emitter writes it: the base is
// saved while the index is loaded and copied into rcx, then restored. P12
// and P5 together leave a load into each register.
func TestPeepholeIndexOperandsLoadDirectly(t *testing.T) {
	got := runPeephole(
		"\tmov rax, [rbp-8]",
		"\tpush rax",
		"\tmov rax, [rbp-24]",
		"\tpush rax",
		"\tpop rcx",
		"\tmov ecx, ecx",
		"\tpop rax",
		"\ttest rax, 1",
	)
	if !sameLines(got, "\tmov rax, [rbp-8]", "\tmov ecx, [rbp-24]", "\ttest rax, 1") {
		t.Errorf("got %q", got)
	}
}

func TestPeepholeDropsReloadAfterCompareAndBranch(t *testing.T) {
	got := runPeephole(
		"\tmov [rbp-32], rax",
		"\tcmp eax, 48",
		"\tjb .L1",
		"\tmov rax, [rbp-32]",
	)
	if !sameLines(got, "\tmov [rbp-32], rax", "\tcmp eax, 48", "\tjb .L1") {
		t.Errorf("got %q", got)
	}
	// Through a test as well, and through more than one branch.
	got = runPeephole(
		"\tmov [rbp-32], rax",
		"\ttest eax, eax",
		"\tjz .L1",
		"\tcmp eax, 57",
		"\tja .L1",
		"\tmov rax, [rbp-32]",
	)
	if strings.Contains(strings.Join(got, "\n"), "mov rax, [rbp-32]") {
		t.Errorf("reload survived: %q", got)
	}

	decline := [][]string{
		// A label between: the reload is reachable from a branch that did not
		// execute the store.
		{"\tmov [rbp-32], rax", "\tcmp eax, 48", "\tjb .L1", ".L2:", "\tmov rax, [rbp-32]"},
		// An unconditional jump: the store's value never reaches the reload
		// by fall-through, and the rule only argues about fall-through.
		{"\tmov [rbp-32], rax", "\tcmp eax, 48", "\tjmp .L1", "\tmov rax, [rbp-32]"},
		// A different slot.
		{"\tmov [rbp-32], rax", "\tcmp eax, 48", "\tjb .L1", "\tmov rax, [rbp-40]"},
		// A write to the accumulator between.
		{"\tmov [rbp-32], rax", "\tmov eax, 3", "\tcmp eax, 48", "\tjb .L1", "\tmov rax, [rbp-32]"},
		// A different destination: P8's register form is for the adjacent
		// pair only.
		{"\tmov [rbp-32], rax", "\tcmp eax, 48", "\tjb .L1", "\tmov rcx, [rbp-32]"},
	}
	for _, c := range decline {
		got := runPeephole(c...)
		if !sameLines(got, c...) {
			t.Errorf("declined shape was rewritten:\n  in  %q\n  out %q", c, got)
		}
	}
}

// End to end: a scan loop's `c >= 48 && c <= 57` test reads the byte once.
func TestScanLoopReadsEachByteOnce(t *testing.T) {
	asm := compile(t, `@noinline function digits(s: string): i32 {
  var n: i32 = 0;
  var i: i32 = 0;
  while (i < s.len()) {
    var c = s[i];
    if (c >= 48 && c <= 57) { n = n + 1; }
    i = i + 1;
  }
  return n;
}
function main(): i32 { return digits("a1b22"); }`)
	body := fnBody(t, asm, "digits")
	for _, bad := range []string{"mov ecx, ecx", "setae", "setbe", "movzx eax, al", "push rax"} {
		if strings.Contains(body, bad) {
			t.Errorf("%q survives in the scan loop:\n%s", bad, body)
		}
	}
	// The byte is stored to c's slot and compared twice from the register.
	if n := strings.Count(body, "cmp eax, 48") + strings.Count(body, "cmp eax, 57"); n != 2 {
		t.Errorf("want the two compares against the accumulator, got %d:\n%s", n, body)
	}
}
