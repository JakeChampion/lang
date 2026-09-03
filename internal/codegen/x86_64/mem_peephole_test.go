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
	// P11 removed the reload of the counter that the next increment
	// overwrites; only the one a label follows survives.
	if n := strings.Count(on, "mov rax, [rbp-24]"); n != 1 {
		t.Errorf("P11: expected 1 surviving reload of [rbp-24], got %d:\n%s", n, on)
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
