package x86_64

// Tests for P18 and P5, the two rules that remove the operand-stack round
// trip around a value computed while the accumulator holds something else.
//
//   P18   push rax / OP… / mov REG, rax / pop rax
//           => OP… renamed onto REG
//     Exactly equivalent — both forms leave rax holding the saved value, so
//     nothing has to be known about what comes next.
//
//   P5    push rax / OP / mov REG, rax / pop DST / call f
//           => mov DST, rax / OP written to REG / call f
//     Leaves rax holding the saved value where the original left it holding
//     the materialised one, so it needs rax to be dead. The call is the proof.

import (
	"regexp"
	"strings"
	"testing"
)

func TestPeepholeRenamesMaterialisationOntoItsRegister(t *testing.T) {
	cases := []struct {
		name string
		op   string
		mv   string
		want []string // nil means refused
	}{
		{"constant to a scratch register", "\tmov eax, 5", "\tmov rcx, rax", []string{"\tmov ecx, 5"}},
		{"wide constant keeps its width", "\tmovabs rax, 5", "\tmov rcx, rax", []string{"\tmovabs rcx, 5"}},
		{"64-bit load", "\tmov rax, [rbp-8]", "\tmov rcx, rax", []string{"\tmov rcx, [rbp-8]"}},
		{"32-bit load renames to the 32-bit name", "\tmov eax, [rbp-8]", "\tmov rsi, rax", []string{"\tmov esi, [rbp-8]"}},
		{"rip-relative address", "\tlea rax, [rip + .Lc0]", "\tmov rdx, rax", []string{"\tlea rdx, [rip + .Lc0]"}},
		{"self-xor zero", "\txor eax, eax", "\tmov rdi, rax", []string{"\txor edi, edi"}},
		{"zero extension", "\tmovzx eax, byte ptr [rbp-8]", "\tmov rcx, rax", []string{"\tmovzx ecx, byte ptr [rbp-8]"}},
		{"extended register name", "\tmov eax, 7", "\tmov r8, rax", []string{"\tmov r8d, 7"}},

		// A 32-bit copy out of the accumulator (P12's output) zero-extends:
		// a 64-bit load narrows to a load of the same low bytes, which
		// zero-extends as the copy did.
		{"32-bit copy narrows a 64-bit load", "\tmov rax, [rbp-8]", "\tmov ecx, eax", []string{"\tmov ecx, [rbp-8]"}},
		{"32-bit copy of a constant", "\tmov eax, 5", "\tmov esi, eax", []string{"\tmov esi, 5"}},
		{"32-bit copy of a zero extension", "\tmovzx eax, byte ptr [rbp-8]", "\tmov ecx, eax", []string{"\tmovzx ecx, byte ptr [rbp-8]"}},
		{"32-bit copy into an extended register", "\tmov rax, [rbp-8]", "\tmov r8d, eax", []string{"\tmov r8d, [rbp-8]"}},
		// An address or a sign extension to 64 bits has no 32-bit form that
		// keeps the value the copy took, so the copy's zero extension stays.
		{"32-bit copy of an address", "\tlea rax, [rip + .Lc0]", "\tmov ecx, eax", []string{"\tlea rcx, [rip + .Lc0]", "\tmov ecx, ecx"}},
		{"32-bit copy of a 64-bit sign extension", "\tmovsxd rax, dword ptr [rbp-8]", "\tmov ecx, eax", []string{"\tmovsxd rcx, dword ptr [rbp-8]", "\tmov ecx, ecx"}},
		{"32-bit copy into eax itself is not a move out", "\tmov eax, 5", "\tmov eax, eax", nil},

		// An rsp-relative operand means something different once the push is
		// gone — rsp differs by 8 between the two forms.
		{"rsp-relative source is refused", "\tmov rax, [rsp+8]", "\tmov rcx, rax", nil},
		{"esp-relative source is refused", "\tmov eax, [esp+8]", "\tmov rcx, rax", nil},

		// Not a computation that starts by overwriting the accumulator.
		{"an ALU op on the saved value is not a materialisation", "\tadd rax, rcx", "\tmov rsi, rax", nil},
		{"a call is not a materialisation", "\tcall __fn_f", "\tmov rsi, rax", nil},
		{"a copy to rax itself is not a move out", "\tmov eax, 5", "\tmov rax, rax", nil},
		{"rsp is not a value register", "\tmov eax, 5", "\tmov rsp, rax", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := []string{"\tpush rax", c.op, c.mv, "\tpop rax", "\tret"}
			got := runPeephole(in...)
			if c.want == nil {
				if !sameLines(got, in...) {
					t.Fatalf("fold should have been refused, got %v", got)
				}
				return
			}
			if !sameLines(got, append(c.want, "\tret")...) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// P18 renames a computation of any length, and a 32-bit copy narrows only
// the last line, and only where the low half of its result depends on the
// low halves of its inputs alone.
func TestPeepholeRenamesMultiLineOperand(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"index arithmetic feeding a bounds check",
			[]string{"\tpush rax", "\tmov rax, [rbp-456]", "\tsub rax, 1", "\tmov ecx, eax", "\tpop rax", "\tcmp ecx, [rax - 4]"},
			[]string{"\tmov rcx, [rbp-456]", "\tsub ecx, 1", "\tcmp ecx, [rax - 4]"}},
		{"field load through the operand",
			[]string{"\tpush rax", "\tmov rax, [rbp-472]", "\tmov eax, [rax + 16]", "\tadd rax, 2", "\tmovsx rax, eax", "\tmov rcx, rax", "\tpop rax", "\ttest rcx, rcx"},
			[]string{"\tmov rcx, [rbp-472]", "\tmov ecx, [rcx + 16]", "\tadd rcx, 2", "\tmovsx rcx, ecx", "\ttest rcx, rcx"}},
		{"32-bit copy after a 32-bit last line needs nothing",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\tmov eax, [rax + 16]", "\tmov ecx, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rcx, [rbp-8]", "\tmov ecx, [rcx + 16]", "\tret"}},
		{"32-bit copy of a sign extension from the low half is a zero extension",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\tadd rax, 2", "\tmovsx rax, eax", "\tmov ecx, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rcx, [rbp-8]", "\tadd rcx, 2", "\tmov ecx, ecx", "\tret"}},
		{"32-bit copy after a 64-bit memory operand zero-extends explicitly",
			[]string{"\tpush rax", "\tmov rax, [rbp-40]", "\tsub rax, qword ptr [rbp-192]", "\tmov ecx, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rcx, [rbp-40]", "\tsub rcx, qword ptr [rbp-192]", "\tmov ecx, ecx", "\tret"}},
		{"32-bit copy after a right shift zero-extends explicitly",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\tsar rax, 3", "\tmov ecx, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rcx, [rbp-8]", "\tsar rcx, 3", "\tmov ecx, ecx", "\tret"}},
		{"multiply by a constant narrows",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\timul rax, rax, 50", "\tmov ecx, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rcx, [rbp-8]", "\timul ecx, ecx, 50", "\tret"}},
		{"shift count in cl renames onto another register",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\tshl rax, cl", "\tmov rsi, rax", "\tpop rax", "\tret"},
			[]string{"\tmov rsi, [rbp-8]", "\tshl rsi, cl", "\tret"}},
		{"32-bit copy after a shift by cl keeps the 64-bit count mask",
			[]string{"\tpush rax", "\tmov rax, [rbp-8]", "\tshl rax, cl", "\tmov esi, eax", "\tpop rax", "\tret"},
			[]string{"\tmov rsi, [rbp-8]", "\tshl rsi, cl", "\tmov esi, esi", "\tret"}},
	}
	for _, c := range cases {
		got := runPeephole(c.in...)
		if !sameLines(got, c.want...) {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// P18 stands down when a line of the computation is not register
// arithmetic on the accumulator, or names the destination register, an
// 8- or 16-bit accumulator name, or the stack pointer.
func TestPeepholeLeavesOperandOnStackWhenNotRenameable(t *testing.T) {
	cases := [][]string{
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tmov [rbp-16], rax", "\tmov rcx, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tmov eax, [rax + rcx*4]", "\tmov rcx, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tshl rax, cl", "\tmov rcx, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tmovzx eax, al", "\tmov rcx, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tadd rax, qword ptr [rsp + 8]", "\tmov rcx, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tcmovz rax, rcx", "\tmov rsi, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tidiv rcx", "\tmov rsi, rax", "\tpop rax", "\tret"},
		{"\tpush rax", "\tmov rax, [rbp-8]", "\tjae .Loob_3", "\tmov rsi, rax", "\tpop rax", "\tret"},
	}
	for i, c := range cases {
		got := runPeephole(c...)
		if !sameLines(got, c...) {
			t.Errorf("case %d rewritten:\n%s", i, strings.Join(got, "\n"))
		}
	}
}

func TestFoldStackedMaterialiseRestoreElsewhere(t *testing.T) {
	const push, pop, call = "\tpush rax", "\tpop rdi", "\tcall __fn_f"

	got, ok := foldStackedMaterialise(push, "\tmov eax, 5", "\tmov rsi, rax", pop, call)
	if !ok {
		t.Fatal("two-argument call setup was not folded")
	}
	want := []string{"\tmov rdi, rax", "\tmov esi, 5"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}

	// Without the call there is nothing proving rax dead.
	if _, ok := foldStackedMaterialise(push, "\tmov eax, 5", "\tmov rsi, rax", pop, "\tret"); ok {
		t.Error("folded a restore-elsewhere with no call to prove rax dead")
	}
	// Both destinations the same would reorder the two writes.
	if _, ok := foldStackedMaterialise(push, "\tmov eax, 5", "\tmov rdi, rax", pop, call); ok {
		t.Error("folded a case where the copy and the restore target the same register")
	}
	// The materialisation reads the register the restore now writes first.
	if _, ok := foldStackedMaterialise(push, "\tmov rax, [rdi+8]", "\tmov rsi, rax", pop, call); ok {
		t.Error("folded a materialisation that reads the restore's destination")
	}
}

// End to end: a two-argument call with a literal second argument is the
// single most common shape in the corpus, and it must not touch the stack.
func TestTwoArgumentCallNeedsNoOperandStack(t *testing.T) {
	asm := compileOpts(t, `
@noinline function pair(a: i32, b: i32): i32 { return a - b; }
function main(): i32 {
  var t: i32 = 0;
  var i: i32 = 0;
  while (i < 4) { t = t + pair(i, 7); i = i + 1; }
  return t;
}`, Options{})
	body, ok := fnBodyOf(asm, "main")
	if !ok {
		t.Fatal("main not found in emitted asm")
	}
	// The whole argument setup is now three instructions with no stack
	// traffic of its own: the loop counter's frame slot is read straight
	// into its argument register. The accumulator `t` is still pushed
	// around the call, and must be — the call writes rax.
	want := regexp.MustCompile(`\tmov rdi, \[rbp-\d+\]\n\tmov esi, 7\n\tcall __fn_pair\n`)
	if !want.MatchString(body) {
		t.Errorf("argument setup is not the direct three-instruction form:\nwant:\n%s\ngot:\n%s", want, body)
	}
}
