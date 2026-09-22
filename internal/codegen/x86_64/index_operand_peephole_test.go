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

func TestFoldLeaIntoLoad(t *testing.T) {
	cases := []struct{ lea, load, want string }{
		{"\tlea rax, [rax + rcx]", "\tmovzx eax, byte ptr [rax]", "\tmovzx eax, byte ptr [rax + rcx]"},
		{"\tlea rax, [rax + rcx*4]", "\tmov eax, [rax]", "\tmov eax, [rax + rcx*4]"},
		{"\tlea rax, [rax + rcx*8]", "\tmov rax, [rax]", "\tmov rax, [rax + rcx*8]"},
		{"\tlea rax, [rax + rcx]", "\tmovsx eax, byte ptr [rax]", "\tmovsx eax, byte ptr [rax + rcx]"},
	}
	for _, c := range cases {
		got, ok := foldLeaIntoLoad(c.lea, c.load)
		if !ok || got != c.want {
			t.Errorf("foldLeaIntoLoad(%q, %q) = %q, %v; want %q", c.lea, c.load, got, ok, c.want)
		}
	}
	decline := []struct{ lea, load, why string }{
		{"\tlea rax, [rax + rcx*4]", "\tmov rdi, [rax]", "the load leaves the biased address live in rax"},
		{"\tlea rax, [rax + rcx*4]", "\tmovsd xmm0, [rax]", "a float load leaves rax live"},
		{"\tlea rax, [rax + rcx*4]", "\tmov [rax], ecx", "a store is not a load"},
		{"\tlea rax, [rax + 8]", "\tmov eax, [rax]", "not an index form (P7's job)"},
		{"\tlea rdx, [rax + rcx*4]", "\tmov eax, [rax]", "the address is not in rax"},
		{"\tlea rax, [rax + rcx*4]", "\tmov eax, [rax + 4]", "the load already has a displacement"},
	}
	for _, c := range decline {
		if got, ok := foldLeaIntoLoad(c.lea, c.load); ok {
			t.Errorf("folded %q / %q to %q; should decline: %s", c.lea, c.load, got, c.why)
		}
	}
}

func TestPeepholeDropsReloadAfterLoadCompareAndBranch(t *testing.T) {
	got := runPeephole(
		"\tmov rax, [rbp-64]",
		"\tmov rcx, [rbp-72]",
		"\tcmp eax, ecx",
		"\tjne .L1",
		"\tmov rax, [rbp-64]",
	)
	// P14 has folded the right operand's load into the compare.
	if !sameLines(got, "\tmov rax, [rbp-64]", "\tcmp eax, dword ptr [rbp-72]", "\tjne .L1") {
		t.Errorf("got %q", got)
	}
	got = runPeephole(
		"\tmov rax, [rbp-64]",
		"\tmov rcx, rdx",
		"\tcmp eax, ecx",
		"\tjne .L1",
		"\tmov rax, [rbp-64]",
	)
	if !sameLines(got, "\tmov rax, [rbp-64]", "\tmov rcx, rdx", "\tcmp eax, ecx", "\tjne .L1") {
		t.Errorf("got %q", got)
	}
	decline := [][]string{
		// A store between could have written the slot.
		{"\tmov rax, [rbp-64]", "\tmov [rbp-64], rcx", "\tcmp eax, 1", "\tjne .L1", "\tmov rax, [rbp-64]"},
		// A call between clobbers rax.
		{"\tmov rax, [rbp-64]", "\tcall __fn_f", "\tcmp eax, 1", "\tjne .L1", "\tmov rax, [rbp-64]"},
		// A load into a 32-bit register name is not on the whitelist.
		{"\tmov rax, [rbp-64]", "\tmov ecx, [rbp-72]", "\tmov eax, ecx", "\tjne .L1", "\tmov rax, [rbp-64]"},
	}
	for _, c := range decline {
		got := runPeephole(c...)
		if !sameLines(got, c...) {
			t.Errorf("declined shape was rewritten:\n  in  %q\n  out %q", c, got)
		}
	}
}

func TestPeepholeDropsDeadReloadThroughFusedIncrement(t *testing.T) {
	// Two fused increments in a row: the first one's reload is dead because
	// the second one's overwrites rax without reading it.
	got := runPeephole(
		"\tadd qword ptr [rbp-48], 1",
		"\tmov rax, [rbp-48]",
		"\tadd qword ptr [rbp-56], 1",
		"\tmov rax, [rbp-56]",
	)
	if !sameLines(got, "\tadd qword ptr [rbp-48], 1", "\tadd qword ptr [rbp-56], 1", "\tmov rax, [rbp-56]") {
		t.Errorf("got %q", got)
	}
	// A register-form add reads rax: the load stays.
	in := []string{"\tmov rax, [rbp-48]", "\tadd rax, 1", "\tmov rax, [rbp-56]"}
	if got := runPeephole(in...); !sameLines(got, in...) {
		t.Errorf("load feeding an add was dropped: %q", got)
	}
}

// The index helper's common path is straight-line: the bounds check is a
// compare against the length in memory and a branch to an abort that sits
// after the function's ret, as does the inline-string arm, and the element
// load carries the scaled address itself.
func TestIndexHelperColdArmsFollowTheEpilogue(t *testing.T) {
	asm := compile(t, `@noinline function f(s: string, a: i32[], i: i32): i32 {
  return (s[i] as i32) + a[i];
}
function main(): i32 { var a: i32[] = [1, 2]; return f("ab", a, 1); }`)
	body := fnBody(t, asm, "f")
	ret := strings.Index(body, "\tret\n")
	if ret < 0 {
		t.Fatalf("no ret in body:\n%s", body)
	}
	hot, cold := body[:ret], body[ret:]
	for _, want := range []string{"cmp ecx, [rax - 4]", "jae .Loob_", "movzx eax, byte ptr [rax + rcx]", "mov eax, [rax + rcx*4]"} {
		if !strings.Contains(hot, want) {
			t.Errorf("hot path lacks %q:\n%s", want, hot)
		}
	}
	for _, bad := range []string{"__fern_report", "__fern_str_idx_scratch", "shr edx, 1", "\tjmp .Lstridx"} {
		if strings.Contains(hot, bad) {
			t.Errorf("hot path still carries %q:\n%s", bad, hot)
		}
		if !strings.Contains(cold, bad) {
			t.Errorf("cold section lacks %q:\n%s", bad, cold)
		}
	}
	if strings.Contains(hot, "\tlea rax, [rax + rcx") {
		t.Errorf("an index lea survived unfused:\n%s", hot)
	}
}

func TestFoldSlotIntoAlu(t *testing.T) {
	cases := []struct{ load, alu, want string }{
		{"\tmov rcx, [rbp-16]", "\tcmp eax, ecx", "\tcmp eax, dword ptr [rbp-16]"},
		{"\tmov rcx, [rbp-16]", "\tcmp rax, rcx", "\tcmp rax, qword ptr [rbp-16]"},
		{"\tmov rcx, [rbp-16]", "\tadd rax, rcx", "\tadd rax, qword ptr [rbp-16]"},
		{"\tmov rcx, [rbp-16]", "\tsub eax, ecx", "\tsub eax, dword ptr [rbp-16]"},
		{"\tmov rcx, [rbp-16]", "\timul rax, rcx", "\timul rax, qword ptr [rbp-16]"},
	}
	for _, c := range cases {
		got, ok := foldSlotIntoAlu(c.load, c.alu)
		if !ok || got != c.want {
			t.Errorf("foldSlotIntoAlu(%q, %q) = %q, %v; want %q", c.load, c.alu, got, ok, c.want)
		}
	}
	decline := []struct{ load, alu, why string }{
		{"\tmov rcx, [rbp-16]", "\tshl rax, cl", "a shift count is not an operand"},
		{"\tmov rcx, [rbp-16]", "\tcmp ecx, eax", "the slot is the left operand"},
		{"\tmov rcx, [rbp-16]", "\tmov [rax], ecx", "a store reads rcx as data"},
		{"\tmov rcx, [rax]", "\tcmp eax, ecx", "not a frame slot"},
		{"\tmov rcx, [rbp-16 + rdx]", "\tcmp eax, ecx", "not a plain slot"},
		{"\tmov rdx, [rbp-16]", "\tcmp eax, ecx", "a different register"},
		{"\tmov rcx, [rbp-16]", "\tdiv rcx", "a divisor is read by an instruction with no memory form here"},
	}
	for _, c := range decline {
		if got, ok := foldSlotIntoAlu(c.load, c.alu); ok {
			t.Errorf("folded %q / %q to %q; should decline: %s", c.load, c.alu, got, c.why)
		}
	}
}
