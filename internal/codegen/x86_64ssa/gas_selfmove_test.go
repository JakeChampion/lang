package x86_64ssa

import "testing"

// A 64-bit register-to-itself move does nothing and is dropped before it
// reaches the emitted text (#6979 item 4).
//
// The sub-64-bit rows are the ones that matter. `mov eax, eax` looks identical
// to a naive "the operands are equal" rule but zero-extends into the upper 32
// bits, and truncOrExt emits precisely that as the u32 conversion — so a filter
// keyed on operand equality alone would delete a load-bearing instruction and
// silently miscompile every u32 narrowing. Width is the whole condition.
func TestDeadSelfMoveDropsOnly64Bit(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"\tmov rax, rax", true},
		{"mov r15, r15", true},
		{"\tmov rdi, rdi", true},

		// Not self-moves.
		{"\tmov rax, rbx", false},
		{"\tmov rax, [rbp - 8]", false},
		{"\tmov rax, 0", false},

		// Self-moves that DO work — never droppable.
		{"\tmov eax, eax", false},
		{"\tmov r8d, r8d", false},
		{"\tmov ax, ax", false},
		{"\tmov al, al", false},

		// Not a mov at all.
		{"\tadd rax, rax", false},
		{"\tmovzx rax, al", false},
		{".L_fn_f_b0:", false},
	} {
		if got := isDeadSelfMove(tc.line); got != tc.want {
			t.Errorf("isDeadSelfMove(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
