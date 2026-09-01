package x86_64

// Tests for P5, which removes the operand-stack round trip around a value the
// intervening instructions never disturb.
//
// Two forms, with different soundness arguments:
//
//   restore into rax    push rax / OP / mov REG, rax / pop rax
//                         => OP written to REG
//     Exactly equivalent — both forms leave rax holding the saved value, so
//     nothing has to be known about what comes next.
//
//   restore elsewhere   push rax / OP / mov REG, rax / pop DST / call f
//                         => mov DST, rax / OP written to REG / call f
//     Leaves rax holding the saved value where the original left it holding
//     the materialised one, so it needs rax to be dead. The call is the proof.

import (
	"strings"
	"testing"
)

func TestFoldStackedMaterialiseRestoreIntoAcc(t *testing.T) {
	cases := []struct {
		name string
		op   string
		mv   string
		want string // "" means refused
	}{
		{"constant to a scratch register", "\tmov eax, 5", "\tmov rcx, rax", "\tmov ecx, 5"},
		{"wide constant keeps its width", "\tmovabs rax, 5", "\tmov rcx, rax", ""},
		{"64-bit load", "\tmov rax, [rbp-8]", "\tmov rcx, rax", "\tmov rcx, [rbp-8]"},
		{"32-bit load renames to the 32-bit name", "\tmov eax, [rbp-8]", "\tmov rsi, rax", "\tmov esi, [rbp-8]"},
		{"rip-relative address", "\tlea rax, [rip + .Lc0]", "\tmov rdx, rax", "\tlea rdx, [rip + .Lc0]"},
		{"self-xor zero", "\txor eax, eax", "\tmov rdi, rax", "\txor edi, edi"},
		{"zero extension", "\tmovzx eax, byte ptr [rbp-8]", "\tmov rcx, rax", "\tmovzx ecx, byte ptr [rbp-8]"},
		{"extended register name", "\tmov eax, 7", "\tmov r8, rax", "\tmov r8d, 7"},

		// An rsp-relative operand means something different once the push is
		// gone — rsp differs by 8 between the two forms.
		{"rsp-relative source is refused", "\tmov rax, [rsp+8]", "\tmov rcx, rax", ""},
		{"esp-relative source is refused", "\tmov eax, [esp+8]", "\tmov rcx, rax", ""},

		// Not a pure register write.
		{"an ALU op is not a materialisation", "\tadd rax, rcx", "\tmov rsi, rax", ""},
		{"a call is not a materialisation", "\tcall __fn_f", "\tmov rsi, rax", ""},
		{"a copy to rax itself is not a move out", "\tmov eax, 5", "\tmov rax, rax", ""},
		{"rsp is not a value register", "\tmov eax, 5", "\tmov rsp, rax", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := foldStackedMaterialise("\tpush rax", c.op, c.mv, "\tpop rax", "\tret")
			if c.want == "" {
				if ok {
					t.Fatalf("fold should have been refused, got %v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("fold refused; want %q", c.want)
			}
			if len(got) != 1 || got[0] != c.want {
				t.Errorf("got %v, want [%q]", got, c.want)
			}
		})
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
	// traffic of its own. The accumulator `t` is still pushed around the
	// call, and must be — the call writes rax.
	want := "\tmov rdi, rax\n\tmov esi, 7\n\tcall __fn_pair\n"
	if !strings.Contains(body, want) {
		t.Errorf("argument setup is not the direct three-instruction form:\nwant:\n%s\ngot:\n%s", want, body)
	}
}
