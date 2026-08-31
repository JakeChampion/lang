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
		// The RcFreeDebug (FERN_RC_FREE_DEBUG=1) use-after-free detector traps
		// through `ud2`. Without this encoding the in-process assembler — the
		// DEFAULT for -target x86-64-linux — refused the whole build, so the detector
		// could not be used at all on the production path (it is what pinned
		// down #6021).
		{"ud2", "0f0b"},
		{"cdq", "99"},
		{"cqo", "4899"},
		// RFLAGS save/restore. The x86-64 SSA backend's heap guard is called
		// from bump sites that may keep flags live, and every way to compare its
		// cursor against the limit writes them.
		{"pushfq", "9c"},
		{"popfq", "9d"},
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
		// bsr — floor(log2), used by the allocator's large-block
		// power-of-two size class. REX.W 0F BD /r, the two-operand imul
		// shape.
		{"bsr rcx, rax", "480fbdc8"},
		{"bsr rax, rdi", "480fbdc7"},
		// Bit counting — lzcnt/tzcnt (BMI1) and popcnt (SSE4.2): the same
		// shape behind a MANDATORY F3 that must precede the REX byte.
		// `lzcnt` is bsr's OPCODE — the prefix is the only difference, so
		// the row above and the row below must not collide. F3 after the
		// REX would decode as a stray prefix on bsr: a different answer,
		// silently, and undefined at a zero input rather than faulting.
		{"lzcnt rcx, rax", "f3480fbdc8"},
		{"tzcnt rcx, rax", "f3480fbcc8"},
		{"lzcnt eax, edi", "f30fbdc7"},
		{"tzcnt eax, eax", "f30fbcc0"},
		{"lzcnt r8, r9", "f34d0fbdc1"},
		{"popcnt rax, rax", "f3480fb8c0"},
		{"popcnt eax, eax", "f30fb8c0"},
		{"popcnt rcx, rdx", "f3480fb8ca"},
		{"popcnt r8d, r9d", "f3450fb8c1"},
		{"popcnt rax, qword ptr [rbp-8]", "f3480fb845f8"},
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

// TestEncodeVectorSurface pins the packed-byte instructions the SIMD kernels
// need (docs/ATLAS-PLATFORM-PLAN.md §3). Until these landed the assembler had
// NO vector instructions at all — only the scalar float ops the code generator
// uses to shuttle f64 through xmm — which is why __memchr shipped scalar: its
// SSE2 body assembles under GNU `as` but the in-process assembler, the default
// for -target x86-64-linux, could not encode a single instruction of it.
//
// Every expectation below is the byte sequence GNU `as` produces for the same
// mnemonic, captured from `objdump -d -M intel` rather than derived from the
// manual — the point is to agree with the reference assembler, and hand-decoded
// ModRM/REX is exactly the kind of thing that looks right and is not.
//
// The register choices are deliberate: each form appears once with low
// registers and once with r8+/xmm8+ so the REX.R/REX.B extension bits are
// exercised, since dropping a REX bit still assembles and silently addresses
// the wrong register.
func TestEncodeVectorSurface(t *testing.T) {
	cases := []struct{ src, want string }{
		// Unaligned 128-bit load (0x6F) and STORE (0x7F). The two directions
		// are separate opcodes; using the load form for a store assembles
		// cleanly and reads the wrong address, so both are pinned.
		{"movdqu xmm0, [r8]", "f3410f6f00"},
		{"movdqu xmm3, [rdi + 16]", "f30f6f5f10"},
		{"movdqu [rdi], xmm2", "f30f7f17"},
		{"movdqa xmm1, [rax]", "660f6f08"},
		// Byte-wise equality — the compare half of a memchr block.
		{"pcmpeqb xmm0, xmm1", "660f74c1"},
		{"pcmpeqb xmm9, xmm10", "66450f74ca"},
		// The SSE2 byte-splat chain. pshufb would be one instruction but is
		// SSSE3, outside the declared baseline.
		{"punpcklbw xmm1, xmm1", "660f60c9"},
		{"punpcklwd xmm1, xmm1", "660f61c9"},
		{"pshufd xmm1, xmm1, 0", "660f70c900"},
		{"pshufd xmm5, xmm6, 27", "660f70ee1b"},
		// The bridge out of the vector domain: mask -> GPR. Note ModRM.reg
		// names a GENERAL-PURPOSE register here, which is why it needs its
		// own encoder rather than a table entry.
		{"pmovmskb eax, xmm0", "660fd7c0"},
		{"pmovmskb r10d, xmm11", "66450fd7d3"},
		// Scan-forward, which turns that mask into a lane index. NOT tzcnt:
		// tzcnt is BMI1 and on a pre-BMI1 CPU its F3 prefix is ignored, so it
		// degrades silently to bsf rather than faulting.
		{"bsf eax, eax", "0fbcc0"},
		{"bsf rcx, rdx", "480fbcca"},
		// Bitwise packed ops, for kernels that combine several masks.
		{"por xmm0, xmm1", "660febc1"},
		{"pand xmm2, xmm3", "660fdbd3"},
		{"pxor xmm4, xmm4", "660fefe4"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeShld pins the double-precision left shift, which the trig
// argument reduction uses to extract a 64-bit window from the 2/pi bit table.
//
// Its operand direction is the reverse of every other two-register form here:
// ModRM.reg holds the SOURCE and ModRM.rm the DESTINATION. Encoding it the
// usual way round assembles cleanly and shifts in bits from the wrong
// register, so the low/high register pairs below are chosen to make REX.R and
// REX.B disagree — with the fields swapped, `shld r11, rax, cl` would emit
// REX.R instead of REX.B and address r8..r15 on the wrong side.
//
// Byte sequences captured from `objdump -d -M intel`, as above.
func TestEncodeShld(t *testing.T) {
	cases := []struct{ src, want string }{
		{"shld rsi, rdi, cl", "480fa5fe"},
		{"shld r11, rax, cl", "490fa5c3"}, // REX.B only
		{"shld rax, rdx, cl", "480fa5d0"},
		{"shld r11, r12, cl", "4d0fa5e3"}, // REX.R and REX.B
		{"shld r8, r9, cl", "4d0fa5c8"},
		{"shld rdi, rsi, 17", "480fa4f711"},
		{"shld r15, r14, 1", "4d0fa4f701"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeMul pins the one-operand unsigned multiply (F7 /4), the widening
// form that puts the full 128-bit product in rdx:rax. The trig reduction needs
// it because the signed `imul` already here discards the high half's meaning
// for unsigned limbs of the 2/pi table.
//
// /4 is one ModRM.reg value away from `imul`'s /5, `div`'s /6 and `neg`'s /3,
// and all four share the F7 opcode — so a wrong extension digit assembles and
// runs a different instruction.
func TestEncodeMul(t *testing.T) {
	cases := []struct{ src, want string }{
		{"mul rsi", "48f7e6"},
		{"mul rdi", "48f7e7"},
		{"mul rcx", "48f7e1"},
		{"mul r11", "49f7e3"}, // REX.B
		{"mul r15", "49f7e7"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeAdcSbb pins add-with-carry and subtract-with-borrow, which the
// trig reduction needs to accumulate a 128-bit product across two registers.
//
// They sit between `or` (/1) and `and` (/4) in the same ALU opcode family, so
// a wrong extension digit is a different arithmetic instruction that still
// assembles — and one that silently drops the carry, which is precisely the
// bit these exist to propagate.
func TestEncodeAdcSbb(t *testing.T) {
	cases := []struct{ src, want string }{
		{"adc rax, rdx", "4811d0"},
		{"adc rsi, r10", "4c11d6"}, // REX.R
		{"adc r8, rcx", "4911c8"},  // REX.B
		{"adc r11, r12", "4d11e3"}, // both
		{"sbb rax, rdx", "4819d0"},
		{"sbb rsi, r11", "4c19de"},
		{"sbb r13, r14", "4d19f5"},
		{"adc rax, 5", "4883d005"},
		{"sbb rcx, 7", "4883d907"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeUnaryGroupSizes pins the F6/F7 unary family (not/mul/imul/neg/
// div/idiv) across all four operand widths. The class of miss: the group
// previously emitted F7 unconditionally, so an 8-bit operand silently became
// a 32-bit operation and a 16-bit one lost its 0x66 prefix. Expectations
// captured from GNU as / objdump -d -M intel.
func TestEncodeUnaryGroupSizes(t *testing.T) {
	cases := []struct{ src, want string }{
		{"not rax", "48f7d0"},
		{"not eax", "f7d0"},
		{"not ax", "66f7d0"},
		{"not al", "f6d0"},
		{"not sil", "40f6d6"}, // spl/bpl/sil/dil need an empty REX
		{"not byte ptr [rdi]", "f617"},
		{"not word ptr [rdi]", "66f717"},
		{"not dword ptr [rdi]", "f717"},
		{"not qword ptr [r8]", "49f710"},
		{"mul ecx", "f7e1"},
		{"mul cl", "f6e1"},
		{"mul sil", "40f6e6"},
		{"mul ax", "66f7e0"},
		{"mul byte ptr [rdi]", "f627"},
		{"mul word ptr [rdi]", "66f727"},
		{"mul qword ptr [rbp-8]", "48f765f8"},
		{"imul rcx", "48f7e9"}, // one-operand widening form, /5
		{"imul dil", "40f6ef"},
		{"neg bl", "f6db"},
		{"neg word ptr [rdi]", "66f71f"},
		{"div r9b", "41f6f1"},
		{"idiv byte ptr [rsi]", "f63e"},
		{"idiv word ptr [rsi]", "66f73e"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeAdcSbbForms pins the adc/sbb memory and immediate shapes the
// shared alu() path provides beyond the reg,reg forms in TestEncodeAdcSbb.
// Expectations captured from GNU as / objdump -d -M intel.
func TestEncodeAdcSbbForms(t *testing.T) {
	cases := []struct{ src, want string }{
		{"adc dword ptr [rax], 100", "831064"},
		{"adc rax, [rdi]", "481307"},
		{"adc byte ptr [rdi], 1", "801701"},
		{"sbb qword ptr [rdi], 61", "48831f3d"},
		{"sbb eax, ecx", "19c8"},
		{"adc ax, 5", "6683d005"},
		{"sbb cx, 300", "6681d92c01"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeCmovcc pins the conditional moves (0F 40+cc /r). The condition
// table is shared with jcc/setcc, so one representative per encoding shape
// plus the corner conditions (o, p/np) suffices; the class of miss this
// guards is the reg/mem direction (ModRM.reg is the DESTINATION, unlike
// most MR-form integer ops here) and the width prefixes. Expectations
// captured from GNU as / objdump -d -M intel.
func TestEncodeCmovcc(t *testing.T) {
	cases := []struct{ src, want string }{
		{"cmove rax, rcx", "480f44c1"},
		{"cmovne eax, ecx", "0f45c1"},
		{"cmovl r8, r9", "4d0f4cc1"},
		{"cmovge rax, [rdi+8]", "480f4d4708"},
		{"cmovb ecx, [rsi]", "0f420e"},
		{"cmova r10d, r11d", "450f47d3"},
		{"cmovs rax, rdx", "480f48c2"},
		{"cmovo rcx, rdx", "480f40ca"},
		{"cmovp rax, rbx", "480f4ac3"},
		{"cmovnp r15, r14", "4d0f4bfe"},
		{"cmove ax, cx", "660f44c1"},
		{"cmovg ax, word ptr [rdi]", "660f4f07"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeShiftRotateSizes pins the full C0/C1/D0..D3 shift-and-rotate
// group: the rotates (rol/ror/rcl/rcr) that share the group with
// shl/shr/sar, plus the byte (opcode-1), word (0x66) and memory-destination
// shapes the shift() path previously rejected or, worse, would have encoded
// at the wrong width. Expectations captured from GNU as / objdump.
func TestEncodeShiftRotateSizes(t *testing.T) {
	cases := []struct{ src, want string }{
		{"rol rax, 1", "48d1c0"},
		{"rol eax, 5", "c1c005"},
		{"ror rcx, cl", "48d3c9"},
		{"rcl edx, 3", "c1d203"},
		{"rcr rbx, 1", "48d1db"},
		{"rol al, 1", "d0c0"},
		{"rol al, 3", "c0c003"},
		{"rol cl, cl", "d2c1"},
		{"ror ax, 2", "66c1c802"},
		{"rol word ptr [rdi], 1", "66d107"},
		{"ror byte ptr [rsi], 3", "c00e03"},
		{"rcl dword ptr [rdi], cl", "d317"},
		{"shl byte ptr [rdi], 1", "d027"},
		{"shl byte ptr [rdi], 4", "c02704"},
		{"shr word ptr [rsi], 2", "66c12e02"},
		{"sar qword ptr [rbp-8], 3", "48c17df803"},
		{"shl dword ptr [rdi], cl", "d327"},
		{"sar bl, cl", "d2fb"},
		{"shl sil, 2", "40c0e602"},
		{"shr ax, 1", "66d1e8"},
		{"sar cx, cl", "66d3f9"},
		{"shl r8w, 5", "6641c1e005"}, // 0x66 goes before the REX byte
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeShrdAndWideShld pins shrd (0F AC/AD — shld's right-shift
// mirror, same reversed ModRM direction) and the memory-destination and
// 16-bit shapes of both. A memory destination takes its operand width from
// the REGISTER operand, not a ptr prefix. Expectations captured from GNU
// as / objdump -d -M intel.
func TestEncodeShrdAndWideShld(t *testing.T) {
	cases := []struct{ src, want string }{
		{"shrd rsi, rdi, cl", "480fadfe"},
		{"shrd r11, rax, cl", "490fadc3"},
		{"shrd rdi, rsi, 17", "480facf711"},
		{"shrd eax, ecx, 3", "0facc803"},
		{"shld qword ptr [rdi], rax, cl", "480fa507"},
		{"shld dword ptr [rsi+4], ecx, 7", "0fa44e0407"},
		{"shrd qword ptr [r8], r9, 1", "4d0fac0801"},
		{"shld ax, cx, 5", "660fa4c805"},
		{"shrd ax, cx, cl", "660fadc8"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeBitTestBswap pins the bt/bts/btr/btc family (0F A3/AB/B3/BB
// with a register bit index, 0F BA /4../7 ib with an immediate) and bswap
// (0F C8+rd). The bt reg form's ModRM.reg is the bit-index SOURCE — the
// same reversed direction as shld — and the imm forms of all four share
// one opcode distinguished only by the /digit. Expectations captured from
// GNU as / objdump -d -M intel.
func TestEncodeBitTestBswap(t *testing.T) {
	cases := []struct{ src, want string }{
		{"bt rax, rcx", "480fa3c8"},
		{"bt eax, ecx", "0fa3c8"},
		{"bts rdi, rsi", "480fabf7"},
		{"btr r8, r9", "4d0fb3c8"},
		{"btc rax, rdx", "480fbbd0"},
		{"bt qword ptr [rdi], rax", "480fa307"},
		{"bts dword ptr [rsi], ecx", "0fab0e"},
		{"bt rax, 5", "480fbae005"},
		{"bts rcx, 63", "480fbae93f"},
		{"btr eax, 7", "0fbaf007"},
		{"btc r9, 33", "490fbaf921"},
		{"bt qword ptr [rdi], 5", "480fba2705"},
		{"btr dword ptr [rsi+4], 7", "0fba760407"},
		{"bt ax, cx", "660fa3c8"},
		{"bt ax, 3", "660fbae003"},
		{"bswap eax", "0fc8"},
		{"bswap rax", "480fc8"},
		{"bswap r8", "490fc8"},
		{"bswap r12d", "410fcc"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeAtomics pins the read-modify-write surface: xchg with memory
// (86/87, implicitly locked), xadd (0F C0/C1) and cmpxchg (0F B0/B1) in
// register and memory destinations, and the F0 lock prefix, which — like
// rep — prefixes a recursively-encoded instruction. Expectations captured
// from GNU as / objdump -d -M intel.
func TestEncodeAtomics(t *testing.T) {
	cases := []struct{ src, want string }{
		{"xchg [rdi], rax", "488707"},
		{"xchg rax, [rdi]", "488707"}, // both operand orders, one encoding
		{"xchg [rsi], ecx", "870e"},
		{"xchg byte ptr [rdi], al", "8607"},
		{"xchg [rbp-8], r9", "4c874df8"},
		{"xchg dl, [rsi]", "8616"},
		{"xchg ax, [rdi]", "668707"},
		{"lock add qword ptr [rdi], 1", "f048830701"},
		{"lock inc dword ptr [rsi]", "f0ff06"},
		{"lock xadd qword ptr [rdi], rax", "f0480fc107"},
		{"lock cmpxchg qword ptr [rdi], rsi", "f0480fb137"},
		{"xadd qword ptr [rdi], rax", "480fc107"},
		{"xadd dword ptr [rsi], ecx", "0fc10e"},
		{"xadd byte ptr [rdi], al", "0fc007"},
		{"xadd word ptr [rdi], ax", "660fc107"},
		{"xadd rax, rcx", "480fc1c8"},
		{"xadd eax, r9d", "440fc1c8"},
		{"cmpxchg qword ptr [rdi], rsi", "480fb137"},
		{"cmpxchg dword ptr [rsi], ecx", "0fb10e"},
		{"cmpxchg byte ptr [rdi], cl", "0fb00f"},
		{"cmpxchg word ptr [rdi], cx", "660fb10f"},
		{"cmpxchg rcx, rdx", "480fb1d1"},
		{"cmpxchg r8d, r9d", "450fb1c8"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeIncDecConvert pins inc/dec across widths and memory operands
// (FE /0../1 for bytes, FF otherwise — previously register-only and always
// 32/64-bit) plus the accumulator sign-extensions cbw/cwde/cdqe/cwd.
// Expectations captured from GNU as / objdump -d -M intel.
func TestEncodeIncDecConvert(t *testing.T) {
	cases := []struct{ src, want string }{
		{"inc byte ptr [rdi]", "fe07"},
		{"inc word ptr [rsi]", "66ff06"},
		{"inc dword ptr [rdi]", "ff07"},
		{"inc qword ptr [rbp-8]", "48ff45f8"},
		{"dec byte ptr [r8]", "41fe08"},
		{"dec qword ptr [rdi]", "48ff0f"},
		{"inc al", "fec0"},
		{"inc sil", "40fec6"},
		{"dec ah", "fecc"}, // high-byte register: no REX
		{"inc ax", "66ffc0"},
		{"dec r8w", "6641ffc8"},
		{"inc r10b", "41fec2"},
		{"cbw", "6698"},
		{"cwde", "98"},
		{"cdqe", "4898"},
		{"cwd", "6699"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeTestForms pins the completed test surface: the memory forms
// (84/85 — test is symmetric, so ModRM.reg is always the register whichever
// side it was written on), sized memory-immediate forms, and the 16-bit
// reg,imm form (66 F7 /0 iw), which previously emitted a 32-bit immediate.
//
// For an accumulator destination GNU as picks the short A8/A9 forms
// (`test ax, 258` = 66 a9 02 01); this assembler keeps the uniform F6/F7 /0
// encoding, which objdump -D -b binary confirms decodes to the identical
// instruction. Non-accumulator expectations match GNU as exactly.
func TestEncodeTestForms(t *testing.T) {
	cases := []struct{ src, want string }{
		{"test rax, [rdi]", "488507"},
		{"test [rdi], rax", "488507"},
		{"test [rsi], ecx", "850e"},
		{"test dil, [rax]", "408438"},
		{"test byte ptr [rdi], 1", "f60701"},
		{"test word ptr [rdi], 258", "66f7070201"},
		{"test dword ptr [rsi], 256", "f70600010000"},
		{"test qword ptr [rdi], 512", "48f70700020000"},
		{"test cx, 5", "66f7c10500"},
		{"test ax, 258", "66f7c00201"},      // gas: 66a90201 (equivalent)
		{"test al, 1", "f6c001"},            // gas: a801 (equivalent)
		{"test rax, 256", "48f7c000010000"}, // gas: 48a900010000 (equivalent)
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s", c.src, got, c.want)
		}
	}
}

// TestEncodePushPopIndirect pins push of immediates (6A ib / 68 id picked
// by signed-8 fit), push/pop through memory (FF /6, 8F /0), the 16-bit
// register forms — `push ax` previously encoded as `push rax`, silently —
// and indirect call/jmp through memory (FF /2, FF /4). Expectations
// captured from GNU as / objdump -d -M intel.
func TestEncodePushPopIndirect(t *testing.T) {
	cases := []struct{ src, want string }{
		{"push 5", "6a05"},
		{"push -1", "6aff"},
		{"push 127", "6a7f"},
		{"push 128", "6880000000"},
		{"push 1000", "68e8030000"},
		{"push -129", "687fffffff"},
		{"push qword ptr [rdi]", "ff37"},
		{"push qword ptr [rbp-8]", "ff75f8"},
		{"push qword ptr [r12]", "41ff3424"}, // r12 base forces a SIB byte
		{"push [rax]", "ff30"},               // unsized memory defaults to 64-bit
		{"pop qword ptr [rdi]", "8f07"},
		{"pop qword ptr [r13+8]", "418f4508"}, // r13 base forces a disp
		{"push ax", "6650"},
		{"pop ax", "6658"},
		{"call qword ptr [rax]", "ff10"},
		{"call qword ptr [rbp-16]", "ff55f0"},
		{"call qword ptr [r11+8]", "41ff5308"},
		{"jmp qword ptr [rax]", "ff20"},
		{"jmp qword ptr [rsi+rcx*8]", "ff24ce"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeIndirectThroughRip pins call/jmp through a rip-relative memory
// slot (FF /2 and FF /4 with the mod=00 rm=101 rip encoding). Layout: the
// call is 6 bytes and the jmp 6, ret makes 13, so .rodata starts at
// align8(13)=16 and the disp32s are 16-6=10 and 16-12=4 (rip-relative
// displacements resolve against the END of the instruction).
func TestEncodeIndirectThroughRip(t *testing.T) {
	src := `
call qword ptr [rip + sym]
jmp qword ptr [rip + sym]
ret
.section .rodata
sym:
	.quad 0
`
	want := "ff150a000000" + "ff2504000000" + "c3"
	if got := asm(t, src); got != want {
		t.Errorf("asm rip-indirect block = %s, want %s", got, want)
	}
}

// TestEncodeStringMisc pins the remaining zero-operand surface: the word/
// dword/qword string ops, the zero-operand movsd/cmpsd (the A5/A7 string
// forms — the same mnemonics WITH operands are SSE scalar doubles, so the
// operand count is what picks the encoding), std, and the fence/nop/trap
// miscellany. Expectations captured from GNU as / objdump -d -M intel.
func TestEncodeStringMisc(t *testing.T) {
	cases := []struct{ src, want string }{
		{"stosd", "ab"},
		{"lodsw", "66ad"},
		{"lodsd", "ad"},
		{"lodsq", "48ad"},
		{"scasw", "66af"},
		{"scasd", "af"},
		{"scasq", "48af"},
		{"cmpsw", "66a7"},
		{"movsd", "a5"},
		{"cmpsd", "a7"},
		{"movsd xmm0, xmm1", "f20f10c1"}, // with operands: the SSE form
		{"rep movsd", "f3a5"},
		{"std", "fd"},
		{"nop", "90"},
		{"int3", "cc"},
		{"leave", "c9"},
		{"pause", "f390"},
		{"mfence", "0faef0"},
		{"lfence", "0faee8"},
		{"sfence", "0faef8"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncode16BitOperandSize pins the 0x66 operand-size prefix across every
// encoder that takes a GPR. The class of miss: `add ax, bx` previously
// encoded as `add eax, ebx` — no prefix, a silently 32-bit operation — and
// the same held for every reg/mem/imm form threaded through rmReg/memReg/
// regMem and the mov/alu immediate paths. Expectations captured from GNU
// as / objdump -d -M intel; for an accumulator-with-immediate GNU as picks
// its short form (`add ax, 200` = 66 05 c8 00), where this assembler keeps
// the uniform 81 /0 encoding — objdump -D -b binary confirms it decodes to
// the identical instruction (as does 66 87 c8 for `xchg ax, cx`, where gas
// picks 66 91).
func TestEncode16BitOperandSize(t *testing.T) {
	cases := []struct{ src, want string }{
		{"add ax, bx", "6601d8"},
		{"mov ax, 5", "66b80500"},
		{"cmp word ptr [rdi], ax", "663907"},
		{"mov ax, bx", "6689d8"},
		{"mov ax, [rdi]", "668b07"},
		{"mov [rdi], ax", "668907"},
		{"mov r9w, 258", "6641b90201"},
		{"sub ax, 100", "6683e864"},
		{"xor cx, dx", "6631d1"},
		{"cmp ax, 1000", "6681f8e803"}, // gas: 663de803 (short form, equivalent)
		{"and r8w, r9w", "664521c8"},
		{"or word ptr [rsi], cx", "66090e"},
		{"test ax, bx", "6685d8"},
		{"add cx, 1000", "6681c1e803"},
		{"add cx, 5", "6683c105"},
		{"imul ax, bx", "660fafc3"},
		{"imul cx, word ptr [rdi]", "660faf0f"},
		{"imul ax, bx, 100", "666bc364"},
		{"imul ax, bx, 1000", "6669c3e803"}, // long form takes an imm16, not imm32
		{"lea ax, [rdi+4]", "668d4704"},
		{"movzx eax, ax", "0fb7c0"},
		{"movzx ax, cl", "660fb6c1"},
		{"movzx ax, byte ptr [rdi]", "660fb607"},
		{"movsx cx, byte ptr [rdi]", "660fbe0f"},
		{"movsx ax, cl", "660fbec1"},
		{"popcnt ax, bx", "66f30fb8c3"}, // 0x66 precedes the mandatory F3
		{"lzcnt ax, bx", "66f30fbdc3"},
		{"tzcnt r9w, r10w", "66f3450fbcca"},
		{"bsf ax, bx", "660fbcc3"},
		{"bsr cx, word ptr [rdi]", "660fbd0f"},
		{"xchg cx, dx", "6687d1"},
		{"xchg ax, cx", "6687c8"}, // gas: 6691 (short form, equivalent)
		{"add word ptr [rdi], 200", "668107c800"},
		{"mov al, 5", "b005"},
		{"mov dil, 5", "40b705"},
		{"mov r9b, 200", "41b1c8"},
		{"mov ah, 5", "b405"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s", c.src, got, c.want)
		}
	}
}

// TestEncodeTextAlignment pins .p2align/.balign/.align padding in .text.
// The class of miss: these directives were silently ignored, so a function
// asked to start on a 16-byte boundary did not — harmless for correctness
// in the flat layout, wrong the moment anything derives an address from
// the alignment. Fill bytes are the multi-byte NOPs GNU as emits
// (binutils' alt_patt), verified against `as` + objdump at several
// misalignments: gas caps a single NOP at 11 bytes and repeats it. On
// x86-64 ELF, .align takes a BYTE COUNT exactly like .balign (the
// power-of-two-exponent reading is other targets' convention); .p2align
// takes the exponent. A third argument is max-skip: alignment is skipped
// when it would pad more than that many bytes.
func TestEncodeTextAlignment(t *testing.T) {
	cases := []struct{ src, want string }{
		// 1..6-byte fills after a 1-byte instruction.
		{"ret\n.balign 2\nret", "c390c3"},
		{"ret\n.p2align 2\nret", "c30f1f00c3"},
		{"ret\n.align 4\nret", "c30f1f00c3"}, // .align 4 = 4 BYTES, not 16
		{"ret\n.balign 8\nret", "c30f1f80000000" + "00c3"},
		{"ret\nret\n.balign 8\nret", "c3c3" + "660f1f440000c3"},
		// 15-byte fill: one 11-byte NOP then a 4-byte one, as gas emits.
		{"ret\n.p2align 4\nret", "c3" + "66662e0f1f840000000000" + "0f1f4000" + "c3"},
		// Already aligned: no padding.
		{"ret\nret\n.balign 2\nret", "c3c3c3"},
		// max-skip: 15 bytes needed > 7 allowed, so no padding at all.
		{"ret\n.p2align 4,,7\nret", "c3c3"},
		{"ret\n.p2align 4,,15\nret", "c3" + "66662e0f1f840000000000" + "0f1f4000" + "c3"},
		{"ret\n.balign 8,,3\nret", "c3c3"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeRodataAlignArgs pins the two- and three-argument alignment
// forms in data sections (fill value ignored — data padding is zero
// bytes — and max-skip honoured), which previously failed to parse.
func TestEncodeRodataAlignArgs(t *testing.T) {
	src := `
ret
.section .rodata
.byte 1
.balign 4,0,8
a: .byte 2
.balign 4,,1
b: .byte 3
`
	_, rodata, err := AssembleProgram(src, elf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v", err)
	}
	want := "01000000" + "02" + "03" // padded to 4; second align skipped (3 > 1)
	if got := hex.EncodeToString(rodata); got != want {
		t.Errorf("rodata = %s, want %s", got, want)
	}
}

// TestEncodePackedArithmetic pins the SSE2 packed integer/float table
// additions — all [66|F3|F2] 0F <op> /r with an xmm destination, so the
// risk is a wrong opcode byte per mnemonic, which assembles cleanly and
// runs a different lane operation. One case per mnemonic, with xmm8+
// pairs sprinkled in to exercise REX.R/B. Expectations captured from GNU
// as / objdump -d -M intel.
func TestEncodePackedArithmetic(t *testing.T) {
	cases := []struct{ src, want string }{
		{"paddb xmm0, xmm1", "660ffcc1"},
		{"paddw xmm2, xmm3", "660ffdd3"},
		{"paddd xmm4, xmm5", "660ffee5"},
		{"paddq xmm6, xmm7", "660fd4f7"},
		{"psubb xmm8, xmm9", "66450ff8c1"},
		{"psubw xmm10, xmm11", "66450ff9d3"},
		{"psubd xmm12, xmm13", "66450ffae5"},
		{"psubq xmm14, xmm15", "66450ffbf7"},
		{"paddusb xmm0, xmm8", "66410fdcc0"},
		{"psubusb xmm1, [rdi]", "660fd80f"},
		{"paddsb xmm2, xmm3", "660fecd3"},
		{"psubsb xmm4, xmm5", "660fe8e5"},
		{"pavgb xmm0, xmm1", "660fe0c1"},
		{"pminub xmm0, xmm1", "660fdac1"},
		{"pmaxub xmm2, xmm3", "660fded3"},
		{"pminsw xmm4, xmm5", "660feae5"},
		{"pmaxsw xmm6, xmm7", "660feef7"},
		{"pmullw xmm0, xmm1", "660fd5c1"},
		{"pmulhw xmm2, xmm3", "660fe5d3"},
		{"pmulhuw xmm4, xmm5", "660fe4e5"},
		{"pmuludq xmm6, xmm7", "660ff4f7"},
		{"psadbw xmm0, xmm1", "660ff6c1"},
		{"pandn xmm2, xmm3", "660fdfd3"},
		{"packsswb xmm0, xmm1", "660f63c1"},
		{"packuswb xmm2, xmm3", "660f67d3"},
		{"packssdw xmm4, xmm5", "660f6be5"},
		{"punpckhbw xmm0, xmm1", "660f68c1"},
		{"punpckhwd xmm2, xmm3", "660f69d3"},
		{"punpckldq xmm4, xmm5", "660f62e5"},
		{"punpckhdq xmm6, xmm7", "660f6af7"},
		{"punpcklqdq xmm0, xmm1", "660f6cc1"},
		{"punpckhqdq xmm2, xmm3", "660f6dd3"},
		{"pcmpgtb xmm0, xmm1", "660f64c1"},
		{"pcmpgtw xmm2, xmm3", "660f65d3"},
		{"pcmpgtd xmm4, xmm5", "660f66e5"},
		{"addpd xmm0, xmm1", "660f58c1"},
		{"subpd xmm2, xmm3", "660f5cd3"},
		{"mulpd xmm4, xmm5", "660f59e5"},
		{"divpd xmm6, xmm7", "660f5ef7"},
		{"sqrtpd xmm8, xmm9", "66450f51c1"},
		{"minpd xmm0, xmm1", "660f5dc1"},
		{"maxpd xmm2, xmm3", "660f5fd3"},
		{"addps xmm0, xmm1", "0f58c1"},
		{"subps xmm2, xmm3", "0f5cd3"},
		{"mulps xmm4, xmm5", "0f59e5"},
		{"divps xmm6, xmm7", "0f5ef7"},
		{"sqrtps xmm8, xmm9", "450f51c1"},
		{"minps xmm10, xmm11", "450f5dd3"},
		{"maxps xmm12, xmm13", "450f5fe5"},
		{"andnpd xmm0, xmm1", "660f55c1"},
		{"orpd xmm2, xmm3", "660f56d3"},
		{"andnps xmm4, xmm5", "0f55e5"},
		{"orps xmm6, xmm7", "0f56f7"},
		{"unpcklpd xmm0, xmm1", "660f14c1"},
		{"unpckhpd xmm2, xmm3", "660f15d3"},
		{"cvtdq2ps xmm0, xmm1", "0f5bc1"},
		{"cvtps2dq xmm2, xmm3", "660f5bd3"},
		{"cvttps2dq xmm4, xmm5", "f30f5be5"},
		{"cvtdq2pd xmm6, xmm7", "f30fe6f7"},
		{"cvtpd2dq xmm8, xmm9", "f2450fe6c1"},
		{"cvttpd2dq xmm10, xmm11", "66450fe6d3"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeVectorMovesAndShifts pins movups/movupd in both directions
// (0F 10 load / 0F 11 store — the same silent-wrong-direction trap as
// movdqu) and the vector shifts: by-register counts (sseOps entries) and
// the by-immediate 0F 71/72/73 groups, where the /digit picks the shift
// and the shifted register sits in ModRM.rm. Expectations captured from
// GNU as / objdump -d -M intel.
func TestEncodeVectorMovesAndShifts(t *testing.T) {
	cases := []struct{ src, want string }{
		{"movups xmm0, [rdi]", "0f1007"},
		{"movups [rdi], xmm1", "0f110f"},
		{"movups xmm2, xmm3", "0f10d3"},
		{"movupd xmm0, [rsi]", "660f1006"},
		{"movupd [rsi], xmm9", "66440f110e"},
		{"movupd xmm4, xmm5", "660f10e5"},
		{"psllw xmm0, xmm1", "660ff1c1"},
		{"pslld xmm2, xmm3", "660ff2d3"},
		{"psllq xmm4, xmm5", "660ff3e5"},
		{"psrlw xmm6, xmm7", "660fd1f7"},
		{"psrld xmm8, xmm9", "66450fd2c1"},
		{"psrlq xmm10, xmm11", "66450fd3d3"},
		{"psraw xmm0, xmm1", "660fe1c1"},
		{"psrad xmm2, xmm3", "660fe2d3"},
		{"psllw xmm0, 5", "660f71f005"},
		{"pslld xmm1, 7", "660f72f107"},
		{"psllq xmm2, 63", "660f73f23f"},
		{"psrlw xmm3, 1", "660f71d301"},
		{"psrld xmm4, 31", "660f72d41f"},
		{"psrlq xmm5, 3", "660f73d503"},
		{"psraw xmm6, 15", "660f71e60f"},
		{"psrad xmm7, 2", "660f72e702"},
		{"pslldq xmm8, 4", "66410f73f804"},
		{"psrldq xmm9, 12", "66410f73d90c"},
		{"movmskps eax, xmm0", "0f50c0"},
		{"movmskps r10d, xmm11", "450f50d3"},
		{"movmskpd eax, xmm1", "660f50c1"},
		{"movmskpd r8d, xmm12", "66450f50c4"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestEncodeSSE4Surface pins the three-byte-opcode families: the 66 0F 38
// packed min/max/multiply/ptest table, crc32 (F2 0F 38 F0/F1, whose opcode
// keys on the SOURCE width and whose 66 goes before the mandatory F2),
// the 66 0F 3A immediate forms (roundss/roundsd, pcmpistri/pcmpestri,
// pextr/pinsr with their inverted r/m-is-destination direction), and the
// two legacy pextrw/pinsrw short forms GNU as prefers for register
// operands (0F C5 / 0F C4). Expectations captured from GNU as / objdump.
func TestEncodeSSE4Surface(t *testing.T) {
	cases := []struct{ src, want string }{
		{"ptest xmm0, xmm1", "660f3817c1"},
		{"ptest xmm8, xmm9", "66450f3817c1"},
		{"pmulld xmm0, xmm1", "660f3840c1"},
		{"pmulld xmm2, [rdi]", "660f384017"},
		{"pminsb xmm0, xmm1", "660f3838c1"},
		{"pminsd xmm2, xmm3", "660f3839d3"},
		{"pminuw xmm4, xmm5", "660f383ae5"},
		{"pminud xmm6, xmm7", "660f383bf7"},
		{"pmaxsb xmm8, xmm9", "66450f383cc1"},
		{"pmaxsd xmm10, xmm11", "66450f383dd3"},
		{"pmaxuw xmm12, xmm13", "66450f383ee5"},
		{"pmaxud xmm14, xmm15", "66450f383ff7"},
		{"crc32 eax, cl", "f20f38f0c1"},
		{"crc32 eax, dil", "f2400f38f0c7"},
		{"crc32 r9d, r10b", "f2450f38f0ca"},
		{"crc32 eax, cx", "66f20f38f1c1"},
		{"crc32 eax, ecx", "f20f38f1c1"},
		{"crc32 rax, rcx", "f2480f38f1c1"},
		{"crc32 rax, r11", "f2490f38f1c3"},
		{"crc32 rax, cl", "f2480f38f0c1"},
		{"crc32 eax, byte ptr [rdi]", "f20f38f007"},
		{"crc32 eax, word ptr [rdi]", "66f20f38f107"},
		{"crc32 eax, dword ptr [rdi]", "f20f38f107"},
		{"crc32 rax, qword ptr [rdi]", "f2480f38f107"},
		{"roundss xmm0, xmm1, 1", "660f3a0ac101"},
		{"roundss xmm8, xmm9, 3", "66450f3a0ac103"},
		{"roundsd xmm2, xmm3, 0", "660f3a0bd300"},
		{"pcmpistri xmm0, xmm1, 8", "660f3a63c108"},
		{"pcmpistri xmm2, [rdi], 12", "660f3a63170c"},
		{"pcmpestri xmm0, xmm1, 8", "660f3a61c108"},
		{"pextrb eax, xmm1, 3", "660f3a14c803"},
		{"pextrb byte ptr [rdi], xmm2, 5", "660f3a141705"},
		{"pextrb r10d, xmm12, 7", "66450f3a14e207"},
		{"pextrw eax, xmm1, 2", "660fc5c102"}, // reg dst: legacy 0F C5 form (as gas)
		{"pextrw word ptr [rdi], xmm3, 1", "660f3a151f01"},
		{"pextrd eax, xmm1, 3", "660f3a16c803"},
		{"pextrd dword ptr [rdi], xmm2, 2", "660f3a161702"},
		{"pextrq rax, xmm1, 1", "66480f3a16c801"},
		{"pextrq qword ptr [rdi], xmm2, 0", "66480f3a161700"},
		{"pinsrb xmm0, eax, 3", "660f3a20c003"},
		{"pinsrb xmm1, byte ptr [rdi], 5", "660f3a200f05"},
		{"pinsrw xmm0, eax, 2", "660fc4c002"},
		{"pinsrw xmm2, word ptr [rdi], 3", "660fc41703"},
		{"pinsrw xmm9, r10d, 5", "66450fc4ca05"},
		{"pinsrd xmm0, ecx, 1", "660f3a22c101"},
		{"pinsrd xmm1, dword ptr [rdi], 2", "660f3a220f02"},
		{"pinsrq xmm0, rax, 1", "66480f3a22c001"},
		{"pinsrq xmm3, qword ptr [rsi], 0", "66480f3a221e00"},
		{"shufps xmm0, xmm1, 0x1b", "0fc6c11b"},
		{"shufps xmm8, xmm9, 0", "450fc6c100"},
		{"shufpd xmm0, xmm1, 1", "660fc6c101"},
		{"shufpd xmm2, [rdi], 2", "660fc61702"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("asm(%q) = %s, want %s (GNU as)", c.src, got, c.want)
		}
	}
}

// TestRejectNearMisses pins the loud-error side: shapes one step away from
// a real instruction, each of which GNU as also rejects (or which would
// otherwise silently encode a different width or register file).
func TestRejectNearMisses(t *testing.T) {
	cases := []string{
		"bswap ax", // 16-bit bswap is architecturally undefined
		"bswap al",
		"cmovz al, cl",     // no 8-bit cmov
		"cmove xmm0, xmm1", // cmov is GPR-only
		"lock",             // prefix with nothing to prefix
		"lock ret",         // hmm: gas rejects lock on non-lockable? see below
		"push eax",         // no 32-bit push in 64-bit mode
		"pop ecx",
		"push byte ptr [rdi]", // push mem is 64-bit only
		"inc [rdi]",           // unsized memory: ambiguous width
		"dec [rax]",
		"shl [rdi], 2",
		"not [rdi]",
		"test byte ptr [rdi], al, 1", // operand-count near-miss
		"test [rdi], 5",              // unsized memory immediate
		"bt [rdi], 5",                // unsized memory bit test
		"bt al, 1",                   // no 8-bit bt
		"imul al, bl",                // no 8-bit two-operand imul
		"movzx ax, cx",               // must widen
		"movzx eax, xmm0",
		"popcnt al, bl", // no 8-bit popcnt
		"lea al, [rdi]",
		"lea xmm0, [rdi]",
		"add xmm0, xmm1", // ALU is GPR-only; xmm needs paddb etc.
		"mov xmm0, rax",  // GPR<->xmm moves are movq/movd
		"inc xmm3",
		"shl xmm1, 2",
		"xadd [rdi], 5", // xadd source must be a register
		"cmpxchg xmm0, xmm1",
		"shld rax, rbx",       // missing count
		"shld rax, rbx, rdx",  // count must be imm or cl
		"shrd al, bl, 1",      // no 8-bit shld/shrd
		"pextrd rax, xmm1, 1", // pextrd is 32-bit; the 64-bit form is pextrq
		"pextrq eax, xmm1, 1",
		"pinsrb xmm0, al, 1", // pinsr sources are r32 (r64 for q)
		"pinsrq xmm0, eax, 1",
		"crc32 ax, cx",      // crc32 destination is r32/r64
		"crc32 rax, ecx",    // 64-bit dst pairs only with r/m8 or r/m64
		"crc32 eax, [rdi]",  // unsized memory source
		"pslldq xmm0, xmm1", // pslldq/psrldq exist only with an immediate
		"movmskps ax, xmm0", // mask destination is r32/r64
		"cvtsi2sd xmm0, ax", // conversion sources are r32/r64
		"cvttsd2si ax, xmm0",
		"movq xmm0, ax",
		"call word ptr [rdi]", // indirect call/jmp is 64-bit only
		"jmp dword ptr [rax]",
		"mul xmm0",
	}
	for _, src := range cases {
		if _, _, err := AssembleProgram(src, elf.TextVAddr); err == nil {
			t.Errorf("asm(%q) unexpectedly assembled; want an error", src)
		}
	}
}
