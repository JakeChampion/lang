package x86_64

// Tests for the memory-shape peepholes P7-P11 (#8194): the address-bias
// fold, the dead reload, the literal shift count, memory-destination ALU and
// the dead accumulator load. Each rule is exercised twice — once as a unit on
// its matcher, where the near-miss shapes it must decline are enumerable, and
// once end to end on a program whose emit contains the shape.

import (
	"strings"
	"testing"
)

// countAdjacent returns how many times a line trimmed-equals a and the
// following line trimmed-equals b.
func countAdjacent(asm, a, b string) int {
	lines := strings.Split(asm, "\n")
	n := 0
	for i := 0; i+1 < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == a && strings.TrimSpace(lines[i+1]) == b {
			n++
		}
	}
	return n
}

func TestFoldAddIntoLoad(t *testing.T) {
	cases := []struct {
		add, load, want string
	}{
		{"\tadd rax, 8", "\tmov rax, [rax]", "\tmov rax, [rax + 8]"},
		{"\tadd rax, 24", "\tmov eax, [rax]", "\tmov eax, [rax + 24]"},
		{"\tadd rax, -16", "\tmov rax, [rax]", "\tmov rax, [rax - 16]"},
	}
	for _, c := range cases {
		got, ok := foldAddIntoLoad(c.add, c.load)
		if !ok || got != c.want {
			t.Errorf("foldAddIntoLoad(%q, %q) = %q, %v; want %q, true", c.add, c.load, got, ok, c.want)
		}
	}

	// The near misses. Each leaves the accumulator's pre-bias value or the
	// `add`'s flags observable, so the fold must decline.
	decline := []struct{ add, load, why string }{
		{"\tadd rax, 8", "\tmov rdi, [rax]", "destination does not overwrite the accumulator"},
		{"\tadd rax, 8", "\tmovzx eax, [rax]", "not the plain load form"},
		{"\tadd rax, 8", "\tmov rax, [rcx]", "load is not through the biased register"},
		{"\tadd rcx, 8", "\tmov rax, [rax]", "bias is not on the accumulator"},
		{"\tadd rax, 8", "\tmov rax, [rax + 8]", "load already has a displacement"},
		{"\tadd rax, rcx", "\tmov rax, [rax]", "bias is not a literal"},
		{"\tadd rax, 4294967296", "\tmov rax, [rax]", "displacement does not fit imm32"},
	}
	for _, c := range decline {
		if got, ok := foldAddIntoLoad(c.add, c.load); ok {
			t.Errorf("foldAddIntoLoad(%q, %q) = %q; want decline (%s)", c.add, c.load, got, c.why)
		}
	}
}

// fieldLoadProg reads a struct field through a reference, which lowers as
// OpAdd(base, offset) followed by a zero-displacement load.
const fieldLoadProg = `struct P { a: i64, b: i64 }
@noinline function get(p: P): i64 { return p.b; }
function main(): i32 { var p = P { a: 1, b: 2 }; return get(p) as i32; }`

func TestPeepholeFoldsAddIntoLoad(t *testing.T) {
	off := compileOpts(t, fieldLoadProg, Options{NoPeephole: true})
	on := compileOpts(t, fieldLoadProg, Options{})

	// The bias itself only exists once P4 has folded the offset constant
	// into an `add rax, K`, so the un-peepholed emit has no pair to count:
	// the displacement load IS the evidence the shape occurred, since
	// nothing else in this program addresses memory off rax with one.
	if !strings.Contains(on, "mov rax, [rax + 8]") {
		t.Errorf("P7: expected a folded `mov rax, [rax + 8]`, got:\n%s", on)
	}
	if n := countAdjacent(on, "add rax, 8", "mov rax, [rax]"); n != 0 {
		t.Errorf("P7: %d unfolded address-bias pairs survived the peephole", n)
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d", instrCount(off), instrCount(on))
	}
}

func TestMatchLoadFromSlot(t *testing.T) {
	if reg, ok := matchLoadFromSlot("\tmov rax, [rbp-16]", "[rbp-16]"); !ok || reg != "rax" {
		t.Errorf(`matchLoadFromSlot(reload) = %q, %v; want "rax", true`, reg, ok)
	}
	if reg, ok := matchLoadFromSlot("\tmov rdi, [rbp-16]", "[rbp-16]"); !ok || reg != "rdi" {
		t.Errorf(`matchLoadFromSlot(other reg) = %q, %v; want "rdi", true`, reg, ok)
	}
	decline := []struct{ line, slot, why string }{
		{"\tmov rax, [rbp-24]", "[rbp-16]", "a different slot"},
		{"\tmov eax, [rbp-16]", "[rbp-16]", "a 32-bit read does not reproduce the stored 64 bits"},
		{"\tmov rsp, [rbp-16]", "[rbp-16]", "rsp is not a value register"},
	}
	for _, c := range decline {
		if reg, ok := matchLoadFromSlot(c.line, c.slot); ok {
			t.Errorf("matchLoadFromSlot(%q, %q) = %q; want decline (%s)", c.line, c.slot, reg, c.why)
		}
	}
}

func TestFoldConstShift(t *testing.T) {
	cases := []struct{ mov, shift, want string }{
		{"\tmov ecx, 3", "\tshl eax, cl", "\tshl eax, 3"},
		{"\tmov rcx, 8", "\tsar rax, cl", "\tsar rax, 8"},
		{"\tmov ecx, 0", "\tshr eax, cl", "\tshr eax, 0"},
	}
	for _, c := range cases {
		if got, ok := foldConstShift(c.mov, c.shift); !ok || got != c.want {
			t.Errorf("foldConstShift(%q, %q) = %q, %v; want %q, true", c.mov, c.shift, got, ok, c.want)
		}
	}
	decline := []struct{ mov, shift, why string }{
		{"\tmov ecx, 3", "\tshl ecx, cl", "the destination is the counter the literal was put in"},
		{"\tmov ecx, 3", "\tshl rcx, cl", "the destination is the counter the literal was put in"},
		{"\tmov ecx, 300", "\tshl eax, cl", "the count does not fit imm8"},
		{"\tmov ecx, -1", "\tshl eax, cl", "a negative count is not the low byte the shift reads"},
		{"\tmov edx, 3", "\tshl eax, cl", "the literal did not go to the counter"},
		{"\tmov ecx, 3", "\tshl eax, 1", "the shift does not read cl"},
		{"\tmov ecx, 3", "\tadd eax, ecx", "not a shift"},
	}
	for _, c := range decline {
		if got, ok := foldConstShift(c.mov, c.shift); ok {
			t.Errorf("foldConstShift(%q, %q) = %q; want decline (%s)", c.mov, c.shift, got, c.why)
		}
	}
}

func TestFoldMemDstAlu(t *testing.T) {
	got, ok := foldMemDstAlu("\tmov rax, [rbp-16]", "\tadd rax, 1", "\tmov [rbp-16], rax")
	want := []string{"\tadd qword ptr [rbp-16], 1", "\tmov rax, [rbp-16]"}
	if !ok || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("foldMemDstAlu = %q, %v; want %q, true", got, ok, want)
	}
	decline := []struct{ load, alu, store, why string }{
		{"\tmov rax, [rbp-16]", "\tadd rax, 1", "\tmov [rbp-24], rax", "stores to a different slot"},
		{"\tmov eax, [rbp-16]", "\tadd rax, 1", "\tmov [rbp-16], rax", "the load is not full width"},
		{"\tmov rax, [rbp-16]", "\tadd rax, rcx", "\tmov [rbp-16], rax", "the operand is not a literal"},
		{"\tmov rax, [rbp-16]", "\timul rax, 3", "\tmov [rbp-16], rax", "imul has no memory-destination form"},
		{"\tmov rax, [rbp-16]", "\tadd rax, 4294967296", "\tmov [rbp-16], rax", "the literal does not fit imm32"},
		{"\tmov rax, [rax]", "\tadd rax, 1", "\tmov [rax], rax", "not a frame slot"},
	}
	for _, c := range decline {
		if got, ok := foldMemDstAlu(c.load, c.alu, c.store); ok {
			t.Errorf("foldMemDstAlu(%q, %q, %q) = %q; want decline (%s)", c.load, c.alu, c.store, got, c.why)
		}
	}
}

func TestWritesAccBeforeReading(t *testing.T) {
	for _, l := range []string{"\tpop rax", "\txor eax, eax", "\tmov rax, [rbp-8]", "\tmov eax, 7", "\tlea rax, [rip + sym]"} {
		if !writesAccBeforeReading(l) {
			t.Errorf("writesAccBeforeReading(%q) = false; want true", l)
		}
	}
	// The accumulator survives all of these — a label because control can
	// arrive from an edge that left something else in rax, a jump because
	// the epilogue it lands on returns rax, and the rest because they read
	// it before or instead of writing it.
	for _, l := range []string{".LblkEnd_4:", "\tjmp .Lret_f_0", "\tret", "\tpush rax", "\tmov rdi, rax",
		"\tmov rax, [rax]", "\tmov rax, [rax + 8]", "\tadd rax, 1", "\tcall __fn_f"} {
		if writesAccBeforeReading(l) {
			t.Errorf("writesAccBeforeReading(%q) = true; want false", l)
		}
	}
}

// counterProg increments two i64 frame slots in a loop, the shape that
// lowers to load / add / store against the same slot.
const counterProg = `@noinline function f(n: i64): i64 {
  var i: i64 = 0;
  var s: i64 = 0;
  while (i < n) { s = s + 2; i = i + 1; }
  return s;
}
function main(): i32 { return f(4) as i32; }`

func TestPeepholeUsesMemoryDestinationAlu(t *testing.T) {
	off := compileOpts(t, counterProg, Options{NoPeephole: true})
	on := compileOpts(t, counterProg, Options{})

	for _, want := range []string{"add qword ptr [rbp-16], 1", "add qword ptr [rbp-24], 2"} {
		if !strings.Contains(on, want) {
			t.Errorf("P10: expected %q in:\n%s", want, on)
		}
	}
	// P11 removed both of P10's reloads: the one the next increment overwrites
	// directly, and the one separated from its overwriter by the block label.
	// What survives of each slot is a real read — [rbp-16] for the loop
	// compare, [rbp-24] for the return value, the latter kept because the
	// epilogue does not write rax before `ret` reads it.
	for _, slot := range []string{"[rbp-16]", "[rbp-24]"} {
		if n := strings.Count(on, "mov rax, "+slot); n != 1 {
			t.Errorf("P11: expected 1 surviving load of %s, got %d:\n%s", slot, n, on)
		}
	}
	// No fused increment may be followed by a reload of the slot it just
	// updated, whatever labels intervene.
	for _, pair := range [][2]string{
		{"add qword ptr [rbp-16], 1", "mov rax, [rbp-16]"},
		{"add qword ptr [rbp-24], 2", "mov rax, [rbp-24]"},
	} {
		if n := countAdjacent(on, pair[0], pair[1]); n != 0 {
			t.Errorf("P11: %d dead reload(s) still follow %q:\n%s", n, pair[0], on)
		}
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d", instrCount(off), instrCount(on))
	}
}

func TestPeepholeFoldsLiteralShiftCount(t *testing.T) {
	on := compileOpts(t, `@noinline function f(x: i32): i32 { return x << 3; }
function main(): i32 { return f(5); }`, Options{})
	body := fnBody(t, on, "f")
	if !strings.Contains(body, "shl eax, 3") {
		t.Errorf("P9: expected `shl eax, 3`, got:\n%s", body)
	}
	if strings.Contains(body, ", cl") {
		t.Errorf("P9: the shift still reads cl:\n%s", body)
	}
}

func TestPeepholeDropsDeadReload(t *testing.T) {
	on := compileOpts(t, counterProg, Options{})
	if n := countAdjacent(on, "mov [rbp-16], rax", "mov rax, [rbp-16]"); n != 0 {
		t.Errorf("P8: %d store/reload pairs survived the peephole:\n%s", n, on)
	}
}

// scanLoopProg is #8425's shape: a per-byte loop whose body is a conditional
// increment, so each fused increment ends a statement and the next statement
// opens with the block labels the `if` left behind.
const scanLoopProg = `@noinline function scan(bs: u8[]): i32 {
  var i: i32 = 0;
  var n: i32 = bs.len();
  var lines: i32 = 0;
  while (i < n) {
    if (bs[i] as i32 == 10) { lines = lines + 1; }
    i = i + 1;
  }
  return lines;
}
function main(): i32 { var a: u8[] = [10, 1, 10]; return scan(a); }`

// A label must not protect a dead reload. Both of this loop's fused increments
// are followed by one, and each reload's overwriter is separated from it only by
// labels — `.LifElse_N` / `.LifEnd_N` from the conditional, `.LblkEnd_N` from
// the loop body — so before the label walk P11 declined at both sites and left
// two dead instructions in the hottest loop the coreutils port has.
//
// They were invisible from a disassembly: P2 had already deleted the only jumps
// to those labels, so the binary showed a fused increment followed by an
// unexplained load, and two identical consecutive loads with nothing between
// them (#8425).
func TestPeepholeLooksThroughLabelsForDeadReloads(t *testing.T) {
	on := compileOpts(t, scanLoopProg, Options{})
	body := fnBody(t, on, "scan")

	// P10 has to have fired for this test to be covering anything: without the
	// fused form there is no reload to be dead.
	for _, slot := range []string{"[rbp-16]", "[rbp-32]"} {
		if !strings.Contains(body, "add qword ptr "+slot+", 1") {
			t.Fatalf("P10 no longer fuses the increment of %s, so this test no longer reaches the label case:\n%s", slot, body)
		}
		if n := countAdjacent(on, "add qword ptr "+slot+", 1", "mov rax, "+slot); n != 0 {
			t.Errorf("%d dead reload(s) still follow the fused increment of %s:\n%s", n, slot, body)
		}
	}
	// The induction variable's two REAL reads survive: one to index bs[i]
	// (read straight into the index register), one for the loop compare.
	// Counted so that a P11 which grew too eager — the failure mode on the
	// other side of this rule — shows up as a missing read rather than as a
	// miscompile someone has to debug.
	if n := strings.Count(body, ", [rbp-16]"); n != 2 {
		t.Errorf("expected the induction variable's 2 real reads to survive, got %d:\n%s", n, body)
	}
}
