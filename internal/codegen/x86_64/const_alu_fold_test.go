package x86_64

// Tests for P4, the peephole rule that folds a constant operand into the ALU
// instruction that consumes it.
//
// The rule's whole risk is operand width. The two constant forms this backend
// emits leave rcx holding DIFFERENT 64-bit values for the same printed
// literal — `movabs rax, K` gives exactly K, `mov eax, K` gives the
// zero-extended uint32 — while `<alu> rax, imm32` sign-extends. Every case
// below where the fold is refused is one of those mismatches, and each was a
// wrong answer rather than a missed optimisation.

import (
	"strings"
	"testing"
)

func TestFoldConstAluWidths(t *testing.T) {
	const (
		push = "\tpush rax"
		mvc  = "\tmov rcx, rax"
		pop  = "\tpop rax"
	)
	cases := []struct {
		name  string
		konst string
		alu   string
		want  string // "" means the fold must be refused
	}{
		// The ordinary shapes.
		{"add i32", "\tmov eax, 5", "\tadd eax, ecx", "\tadd eax, 5"},
		{"add i64", "\tmovabs rax, 5", "\tadd rax, rcx", "\tadd rax, 5"},
		{"sub keeps operand order", "\tmov eax, 7", "\tsub eax, ecx", "\tsub eax, 7"},
		{"cmp i64", "\tmovabs rax, 3000000", "\tcmp rax, rcx", "\tcmp rax, 3000000"},
		{"test i64", "\tmovabs rax, 1", "\ttest rax, rcx", "\ttest rax, 1"},
		{"and i32", "\tmov eax, 255", "\tand eax, ecx", "\tand eax, 255"},
		{"or i32", "\tmov eax, 8", "\tor eax, ecx", "\tor eax, 8"},
		{"xor i32", "\tmov eax, 1", "\txor eax, ecx", "\txor eax, 1"},

		// The two-operand imul has no immediate form, so the fold has to
		// reach for the three-operand multiply-by-constant encoding.
		{"imul becomes three-operand", "\tmov eax, 10", "\timul eax, ecx", "\timul eax, eax, 10"},
		{"imul i64", "\tmovabs rax, 10", "\timul rax, rcx", "\timul rax, rax, 10"},

		// `mov rax, N` is the same instruction as movabs in Intel syntax.
		{"mov rax form", "\tmov rax, 12", "\tadd rax, rcx", "\tadd rax, 12"},

		// Zero is materialised as a self-xor, and still carries a value.
		{"xor-zero is the constant 0", "\txor eax, eax", "\tcmp rax, rcx", "\tcmp rax, 0"},

		// A 32-bit constant zero-extends into rcx; imm32 sign-extends. For a
		// NEGATIVE literal those differ, so a 64-bit operation must refuse:
		// `mov eax, -1` leaves rcx = 0x00000000FFFFFFFF, where `add rax, -1`
		// would add 0xFFFFFFFFFFFFFFFF.
		{"negative i32 into i64 op is refused", "\tmov eax, -1", "\tadd rax, rcx", ""},
		{"negative i32 into i32 op folds", "\tmov eax, -1", "\tadd eax, ecx", "\tadd eax, -1"},
		{"non-negative i32 into i64 op folds", "\tmov eax, 6", "\tadd rax, rcx", "\tadd rax, 6"},

		// A wide literal outside imm32 has no immediate encoding at all.
		{"wide literal into i64 op is refused", "\tmovabs rax, 5000000000", "\tadd rax, rcx", ""},
		{"INT32_MIN still fits imm32", "\tmovabs rax, -2147483648", "\tadd rax, rcx", "\tadd rax, -2147483648"},
		{"one past INT32_MAX is refused", "\tmovabs rax, 2147483648", "\tadd rax, rcx", ""},

		// A 32-bit operation reads only the low half of rcx, which the
		// printed literal always describes — so a wide constant is fine
		// there, truncated to its low 32 bits.
		{"wide literal into i32 op truncates", "\tmovabs rax, 5000000000", "\tadd eax, ecx", "\tadd eax, 705032704"},

		// Shapes that are not this pattern.
		{"shift count travels in cl, not rcx", "\tmov eax, 3", "\tshl eax, cl", ""},
		{"the zero-divisor guard tests rcx against itself", "\tmovabs rax, 97", "\ttest rcx, rcx", ""},
		{"a non-constant second operand", "\tmov rax, [rbp-8]", "\tadd rax, rcx", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := foldConstAlu(push, c.konst, mvc, pop, c.alu)
			if c.want == "" {
				if ok {
					t.Fatalf("fold should have been refused, got %q", got)
				}
				return
			}
			if !ok {
				t.Fatalf("fold refused; want %q", c.want)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The rule keys on the exact five-line shape; anything else must be left
// alone, because the four lines it deletes are only dead in that context.
func TestFoldConstAluRequiresTheWholeShape(t *testing.T) {
	cases := [][5]string{
		{"\tpush rcx", "\tmov eax, 5", "\tmov rcx, rax", "\tpop rax", "\tadd rax, rcx"},
		{"\tpush rax", "\tmov eax, 5", "\tmov rcx, rax", "\tpop rdx", "\tadd rax, rcx"},
		{"\tpush rax", "\tmov eax, 5", "\tmov rdx, rax", "\tpop rax", "\tadd rax, rcx"},
		{"\tpush rax", "\tmov eax, 5", "\tmov rcx, rax", "\tpop rax", "\tadd rax, rdx"},
		{"\tpush rax", "\tmov eax, 5", "\tmov rcx, rax", "\tpop rax", "\tadc rax, rcx"},
	}
	for i, c := range cases {
		if got, ok := foldConstAlu(c[0], c[1], c[2], c[3], c[4]); ok {
			t.Errorf("case %d: folded %v to %q; want refused", i, c, got)
		}
	}
}

// End to end: a binary operation against a literal must reach the immediate
// form, and must not leave the constant-into-rcx handoff behind.
func TestConstOperandReachesImmediateForm(t *testing.T) {
	const src = `
@noinline function bump(x: i64): i64 { return x + 1i64; }
@noinline function scaled(x: i64): i64 { return x * 10i64; }
function main(): i32 {
  var i: i64 = 0i64;
  var s: i64 = 0i64;
  while (i < 7i64) { s = s + bump(i) + scaled(i); i = i + 1i64; }
  return s as i32;
}`
	on := compileOpts(t, src, Options{})
	off := compileOpts(t, src, Options{NoPeephole: true})

	// Precondition: without the peephole the constant really does travel
	// through a register, so the test is measuring the rule and not a
	// change somewhere upstream.
	if !hasAdjacent(off, "mov rcx, rax", "pop rax") && !hasAdjacent(off, "push rax", "pop rcx") {
		t.Fatal("precondition: un-peepholed asm does not hand the constant to rcx")
	}
	if strings.Contains(off, "add rax, 1") {
		t.Fatal("precondition: un-peepholed asm already used an immediate")
	}

	for _, want := range []string{"add rax, 1", "imul rax, rax, 10"} {
		if !strings.Contains(on, want) {
			t.Errorf("peepholed asm is missing %q", want)
		}
	}
	if instrCount(on) >= instrCount(off) {
		t.Errorf("peephole did not reduce instruction count: off=%d on=%d",
			instrCount(off), instrCount(on))
	}
}

// The comparison a branch consumes is fused into `cmp; jcc` by an earlier
// pass; P4 has to compose with that rather than displace it, since a
// loop-bound test is the single most common constant operand in the corpus.
func TestConstOperandFoldsIntoFusedCompare(t *testing.T) {
	const src = `
function main(): i32 {
  var i: i64 = 0i64;
  while (i < 3000000i64) { i = i + 1i64; }
  return (i % 97i64) as i32;
}`
	on := compileOpts(t, src, Options{})
	if !strings.Contains(on, "cmp rax, 3000000") {
		t.Error("loop bound did not reach the immediate compare form")
	}
	if strings.Contains(on, "movabs rax, 3000000") {
		t.Error("loop bound is still materialised into a register")
	}
}
