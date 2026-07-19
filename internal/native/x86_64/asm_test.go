package x86_64

import (
	"encoding/hex"
	"strings"
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
		// bit-scan (used by the allocator's large-block power-of-two class):
		// REX.W 0F BD/BC /r, same shape as the two-operand imul.
		{"bsr rcx, rax", "480fbdc8"},
		{"bsf rcx, rax", "480fbcc8"},
		{"bsr rax, rdi", "480fbdc7"},
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
		// x87 transcendentals (matched against GNU as)
		{"fld1", "d9e8"},
		{"fldln2", "d9ed"},
		{"fldl2e", "d9ea"},
		{"f2xm1", "d9f0"},
		{"fyl2x", "d9f1"},
		{"fscale", "d9fd"},
		{"frndint", "d9fc"},
		{"fsin", "d9fe"},
		{"fcos", "d9ff"},
		{"fld st(0)", "d9c0"},
		{"fstp st(1)", "ddd9"},
		{"fxch st(1)", "d9c9"},
		{"fsub st(1), st(0)", "dce9"},
		{"faddp", "dec1"},
		{"fld qword ptr [rsp]", "dd0424"},
		{"fld qword ptr [rsp+8]", "dd442408"},
		{"fstp qword ptr [rsp]", "dd1c24"},
		{"fmul qword ptr [rsp]", "dc0c24"},
		// high-byte register (htons byte-swap) — must NOT carry a REX prefix
		{"xchg al, ah", "86e0"},
		{"mov al, ah", "88e0"},
		// low-byte registers spl/bpl/sil/dil to/from memory — these regs
		// (4..7) can ONLY be addressed with a REX prefix present; without it
		// the ModRM reg field decodes as ah/ch/dh/bh instead, corrupting the
		// operand. Regression guard: __fern_putchar stores its arg via
		// `mov [rsp], dil`, which was emitted REX-less (== `mov [rsp], bh`)
		// so every putchar wrote a stray 0 byte. Encodings cross-checked
		// against GNU as.
		{"mov [rsp], dil", "40883c24"},
		{"mov [rsp], sil", "40883424"},
		{"mov [rbp-8], bpl", "40886df8"},
		{"mov byte ptr [rax], dil", "408838"},
		{"mov dil, [rsp]", "408a3c24"},
		{"mov sil, [rsp]", "408a3424"},
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

// TestEncodeMovImmToMemSize pins the operand-size of an immediate-to-memory
// `mov` against the `byte/word/dword/qword ptr` prefix. Regression for #3544:
// `mov byte ptr [mem], imm` was emitted as a 4-byte store (C7 + imm32) instead
// of C6 + imm8, so __fern_strcat's 1-byte NUL terminator overran its `len+1`
// buffer by 3 bytes (a layout-dependent heap corruption). Expected encodings
// cross-checked against GNU `as -msyntax=intel`.
func TestEncodeMovImmToMemSize(t *testing.T) {
	cases := []struct{ src, want string }{
		{"mov byte ptr [rdi], 0", "c60700"},
		{"mov word ptr [rdi], 0", "66c7070000"},
		{"mov dword ptr [rdi], 0", "c70700000000"},
		{"mov qword ptr [rdi], 0", "48c70700000000"},
		{"mov byte ptr [rdi], 0x41", "c60741"},
		{"mov word ptr [rax], 0x1234", "66c7003412"},
		{"mov byte ptr [r8], 0", "41c60000"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s", c.src, got, c.want)
		}
	}
}

// ALU-family (cmp/add/sub/and/or/xor) immediate forms honour the operand
// size: a `byte ptr` memory operand (or an 8-bit register) selects the
// 80 /ext ib byte opcode, not the 32-bit 83/81 forms. The regression this
// pins: `cmp byte ptr [rdi], 61` — __fern_env's '=' scan — was encoded as
// `cmp dword ptr [rdi], 61` (83 3f 3d), so the compare read 3 extra bytes
// and env() always returned None in natively-linked binaries. Encodings
// cross-checked against GNU as / objdump (for `cmp al, imm8` GNU as picks
// the equivalent short 3C ib form; 80 /7 ib is the same comparison).
func TestEncodeAluImmSize(t *testing.T) {
	cases := []struct{ src, want string }{
		{"cmp byte ptr [rdi], 61", "803f3d"},
		{"cmp byte ptr [r8], 61", "4180383d"},
		{"cmp byte ptr [rbp-24], 0", "807de800"},
		{"add byte ptr [rdi], 1", "800701"},
		{"cmp word ptr [rdi], 61", "6683 3f3d"},
		{"cmp dword ptr [rdi], 61", "833f3d"},
		{"cmp qword ptr [rdi], 61", "48833f3d"},
		{"cmp al, 61", "80f83d"},
		{"cmp cl, 5", "80f905"},
		{"cmp sil, 5", "4080fe05"},
		{"cmp r9b, 5", "4180f905"},
	}
	for _, c := range cases {
		want := strings.ReplaceAll(c.want, " ", "")
		if got := asm(t, c.src); got != want {
			t.Errorf("asm(%q) = %s, want %s", c.src, got, want)
		}
	}
}

// A rip-relative disp32 is relative to the END of its instruction. For
// lea/mov-load forms the disp32 IS the last field, but the mem,imm forms
// (`add qword ptr [rip+sym], 1`, `mov qword ptr [rip+sym], 0`) append the
// immediate after it — resolving against disp-field-end pointed the
// access immLen bytes past the symbol. The regressions this pins: the
// rc-underflow and leakcheck counters incremented at [sym+1] (a ×256
// count drift) and strbuf_take's `mov qword ptr [rip+__fern_strbuf_len],
// 0` cleared [sym+4..sym+12) instead of the length (so the builder never
// reset on the in-process-assembled path). Layout: .text is 27 bytes,
// .rodata starts at align8(27)=32, sym sits at its head, so the expected
// disps are 32−7=25 (lea), 32−15=17 (add …,imm8), 32−26=6 (mov …,imm32).
// Encodings cross-checked against GNU as / objdump.
func TestEncodeRipRelativeDispIsFromInsnEnd(t *testing.T) {
	src := `
lea rax, [rip + sym]
add qword ptr [rip + sym], 1
mov qword ptr [rip + sym], 0
ret
.section .rodata
sym:
	.quad 0
`
	want := "488d0519000000" + // lea rax, [rip+25]
		"48830511000000" + "01" + // add qword ptr [rip+17], 1
		"48c70506000000" + "00000000" + // mov qword ptr [rip+6], 0
		"c3"
	if got := asm(t, src); got != want {
		t.Errorf("asm rip-relative block = %s, want %s", got, want)
	}
}
