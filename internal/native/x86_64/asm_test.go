package x86_64

import (
	"encoding/hex"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// asm assembles a snippet (default section is .text) and returns the
// .text bytes as a lowercase hex string.
func asm(t *testing.T, src string) string {
	t.Helper()
	text, _, err := AssembleProgram(src, elf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram(%q): %v", src, err)
	}
	return hex.EncodeToString(text)
}

// Hand-verified encodings for the phase-1 instruction surface (cross-
// checked against the System V AMD64 encoding; these are exactly what
// `gcc -masm=intel` / `objdump` produce for the same mnemonics).
func TestEncodeIntegerSurface(t *testing.T) {
	cases := []struct{ src, want string }{
		{"ret", "c3"},
		{"syscall", "0f05"},
		{"cdq", "99"},
		{"cqo", "4899"},
		{"push rbp", "55"},
		{"pop rbp", "5d"},
		{"push r12", "4154"},
		{"mov rbp, rsp", "4889e5"},
		{"mov rsp, rbp", "4889ec"},
		{"sub rsp, 16", "4883ec10"},
		{"add rsp, 16", "4883c410"},
		{"mov [rbp-8], rdi", "48897df8"},
		{"mov rax, [rbp-8]", "488b45f8"},
		{"mov [rsp], rax", "48890424"},
		{"mov rcx, [rsp]", "488b0c24"},
		{"mov rdi, [rsp]", "488b3c24"},
		{"mov eax, 0", "b800000000"},
		{"mov eax, 231", "b8e7000000"},
		{"mov edi, eax", "89c7"},
		{"cmp eax, ecx", "39c8"},
		{"sete al", "0f94c0"},
		{"setl al", "0f9cc0"},
		{"movzx eax, al", "0fb6c0"},
		{"test eax, eax", "85c0"},
		{"sub rax, rcx", "4829c8"},
		{"add rax, rcx", "4801c8"},
		{"imul rax, rcx", "480fafc1"},
		{"neg rax", "48f7d8"},
		{"idiv ecx", "f7f9"},
		{"sar rax, 1", "48d1f8"},
		{"shl rax, 3", "48c1e003"},
		{"movabs rax, 4294967296", "48b80000000001000000"},
		// store-immediate-to-memory: the immediate is the *source*
		// operand (regression guard — it was wrongly read from the dest).
		{"mov dword ptr [rax], 1", "c70001000000"},
		{"mov dword ptr [rax], 2147483648", "c70000000080"},
		{"mov dword ptr [rax-8], 1", "c740f801000000"},
		{"mov qword ptr [rbp-8], 5", "48c745f805000000"},
		{"add dword ptr [rax], 100", "830064"},
		{"and dword ptr [rax], 305419896", "812078563412"},
		{"test edi, 1", "f7c701000000"},
		{"movzx eax, byte ptr [rax]", "0fb600"},
		{"imul rcx, rcx, 20", "486bc914"},
		{"imul eax, eax, 1000", "69c0e8030000"},
		{"call r11", "41ffd3"},
		{"jmp rax", "ffe0"},
		{"repe cmpsb", "f3a6"},
		{"rep stosq", "f348ab"},
		// SSE scalar floats
		{"movq xmm0, rax", "66480f6ec0"},
		{"movq rax, xmm0", "66480f7ec0"},
		{"movd xmm0, eax", "660f6ec0"},
		{"addsd xmm1, xmm0", "f20f58c8"},
		{"subsd xmm1, xmm0", "f20f5cc8"},
		{"mulsd xmm1, xmm0", "f20f59c8"},
		{"divsd xmm1, xmm0", "f20f5ec8"},
		{"sqrtsd xmm0, xmm0", "f20f51c0"},
		{"ucomisd xmm1, xmm0", "660f2ec8"},
		{"cvtsi2sd xmm0, rax", "f2480f2ac0"},
		{"cvtsi2sd xmm0, eax", "f20f2ac0"},
		{"cvttsd2si rax, xmm0", "f2480f2cc0"},
		{"cvtsd2ss xmm0, xmm1", "f20f5ac1"},
		{"roundsd xmm0, xmm1, 0", "660f3a0bc100"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("%-22q = %s, want %s", c.src, got, c.want)
		}
	}
}

// rel32 branch/call targets resolve in the final pass: backward, forward,
// and conditional.
func TestEncodeRelativeBranches(t *testing.T) {
	cases := []struct{ src, want string }{
		{"L:\njmp L", "e9fbffffff"},   // back -5
		{"L:\ncall L", "e8fbffffff"},  // back -5
		{"L:\njz L", "0f84faffffff"},  // back -6
		{"jmp L\nL:", "e900000000"},   // forward 0
		{"jne L\nL:", "0f8500000000"}, // forward 0
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("%-12q = %s, want %s", c.src, got, c.want)
		}
	}
}

func TestUnsupportedInstructionErrors(t *testing.T) {
	if _, _, err := AssembleProgram("vaddpd ymm0, ymm1, ymm2", elf.TextVAddr); err == nil {
		t.Fatalf("expected an error for an unsupported instruction")
	}
}
