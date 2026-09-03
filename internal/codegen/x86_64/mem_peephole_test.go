package x86_64

// Tests for the memory-shape peepholes P7-P10 (#8194): the address-bias
// fold, memory-destination ALU, the dead reload, and the literal shift
// count. Each rule is exercised twice — once as a unit on its matcher, where
// the near-miss shapes it must decline are enumerable, and once end to end on
// a program whose emit contains the shape.

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
