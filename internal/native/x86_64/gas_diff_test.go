package x86_64_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
	"github.com/jakechampion/lang/internal/native/x86_64"
)

// TestAssembleAgainstGNUAs is the byte-exact oracle for the x86-64
// assembler, mirroring the arm64 suite of the same name: each snippet is
// assembled both by AssembleProgram and by GNU as (Intel syntax), and the
// .text bytes must match. This pins the whole parser+encoder stack to an
// independent reference.
//
// Deliberately excluded, because our encoding choice differs from GNU as
// while decoding to the SAME instruction (verified via objdump -D -b binary):
//
//   - jcc/jmp/call to a label: gas relaxes an in-range branch to the rel8
//     form; we emit rel32 only (short-branch relaxation is its own roadmap
//     item). Branch encodings are pinned by TestEncodeRelativeBranches.
//   - accumulator-immediate shortenings: gas encodes `test al/ax/eax/rax, imm`
//     via A8/A9, ALU `al/ax/eax/rax, imm` (when the imm8 form doesn't apply)
//     via the 04/05/0C/0D/… accumulator opcodes, and `xchg ax/eax/rax, reg`
//     as 90+r; we use the general F7 /0, 81 /ext, and 87 /r forms. Snippets
//     keep immediates off the accumulators and xchg off rax/eax/ax.
//
// No other divergences were found across the supported surface.
func TestAssembleAgainstGNUAs(t *testing.T) {
	as, objcopy := findX86Binutils(t)

	cases := map[string]string{
		"moves": "" +
			"mov rax, rcx\nmov r8, r15\nmov ecx, edx\nmov eax, 231\nmov rdx, 5\n" +
			"mov rax, -1\nmov r9, 70000\nmovabs rax, 4294967296\nmov rcx, 0x123456789\n",
		"mov_mem": "" +
			"mov [rbp-8], rdi\nmov rax, [rbp-8]\nmov [rsp], rax\nmov rcx, [rsp]\n" +
			"mov qword ptr [r12], rax\nmov r10, qword ptr [r13+8]\nmov dword ptr [rdi+4], ecx\n" +
			"mov [rsp], dil\nmov sil, [rsp]\nmov byte ptr [rax], r9b\nmov cl, byte ptr [rbx+1]\n",
		"mov_imm_to_mem": "" +
			"mov byte ptr [rdi], 65\nmov word ptr [rax], 0x1234\nmov dword ptr [rbp-8], 7\n" +
			"mov qword ptr [r12], 0\nmov dword ptr [rax], 2147483647\n",
		"alu_reg": "" +
			"add rax, rcx\nsub r8, r9\nand rdx, rsi\nor rbx, rdi\nxor r10, r11\ncmp r12, r13\n" +
			"add eax, ecx\nsub esi, edi\nand r8d, r9d\ncmp eax, ecx\nxor edx, edx\n",
		"alu_imm": "" +
			"add rcx, 5\nsub rdx, 100\nand rsi, -16\nor rdi, 15\nxor r8, 1\ncmp r9, 127\n" +
			"add rcx, 1000000\ncmp rdx, 0x10000\nand r10, 0x7fffffff\nsub ecx, 300\n",
		"alu_mem": "" +
			"add qword ptr [rdi], 100\nsub dword ptr [rsi+4], 1000\ncmp byte ptr [rdi], 61\n" +
			"cmp word ptr [rdi], 61\nand qword ptr [rsp+8], -16\nor dword ptr [r13-4], 15\n" +
			"add [rdi], rax\nsub rax, [rdi+8]\ncmp rcx, qword ptr [rbp-16]\nxor [rsi], edx\n",
		// 128-bit add/sub carried across register pairs.
		"adc_sbb_chain": "" +
			"add rax, rdx\nadc rbx, rcx\nadc r11, r12\nadc rcx, 5\n" +
			"sub rax, rdx\nsbb rbx, rcx\nsbb r13, r14\nsbb rdx, 100\nadc esi, edi\n",
		"byte_alu": "" +
			"add sil, dil\nand cl, dl\nor bl, al\ncmp cl, 5\ncmp sil, 5\ncmp r9b, 5\n" +
			"add byte ptr [rdi], 1\ntest cl, 1\ntest sil, 1\ntest r9b, r9b\nmov al, ah\nxchg al, ah\n",
		// 16-bit operand forms — the family whose missing 0x66 prefix
		// made `mov word ptr [rsp+2], ax` a 4-byte store.
		"word_ops": "" +
			"mov word ptr [rsp+2], ax\nmov ax, word ptr [rsi+2]\nmov ax, cx\nmov r9w, 5\n" +
			"add ax, cx\nadd cx, 5\nadd cx, 300\ncmp word ptr [rdi], 61\ntest cx, 5\n" +
			"inc ax\nneg ax\nshl ax, 3\nmovzx ax, cl\nimul ax, cx, 20\nxchg cx, dx\n",
		"shifts": "" +
			"shl rax, 3\nshr r9, 1\nsar rcx, 63\nshl rdx, cl\nshr rdi, cl\nsar rsi, cl\n" +
			"shr edi, 5\nshl eax, 1\nshl cl, 3\nshr dl, 1\nsar ah, 2\nshl bl, cl\n",
		"shld": "" +
			"shld rsi, rdi, cl\nshld r11, rax, cl\nshld r11, r12, 7\nshld rdi, rsi, 17\n",
		"bt": "" +
			"bt rax, 3\nbt r11, 63\nbt eax, 5\nbt rax, rcx\nbt r8, r9\nbt rdx, r15\n",
		"cmov": "" +
			"cmove rax, rcx\ncmovne r9, r10\ncmovl eax, ecx\ncmovge rax, qword ptr [rdi+8]\n" +
			"cmova rbx, rdx\ncmovbe r12, r13\ncmovs rcx, rdx\ncmovz rdi, rsi\n",
		"setcc": "" +
			"sete al\nsetne r9b\nsetl cl\nsetg sil\nsetge dil\nsetb dl\nsetae bpl\nseta bl\nsets r15b\n",
		"movzx_movsx": "" +
			"movzx eax, al\nmovzx eax, byte ptr [rax]\nmovzx r8, word ptr [rsi+4]\nmovzx rax, cl\n" +
			"movsx rax, cl\nmovsx rdx, word ptr [rdi]\nmovsx eax, byte ptr [rbp-1]\n" +
			"movsxd rax, ecx\nmovsxd r10, dword ptr [rbp-4]\n",
		"lea_sib": "" +
			"lea rax, [rbx+rcx*4+8]\nlea r9, [r12+16]\nlea rdx, [r13]\nlea rcx, [rsp+8]\n" +
			"lea rax, [rcx*8+5]\nlea rsi, [rdi+r8*2]\nlea r11, [r13+r14*8-32]\nlea eax, [rdi+1]\n",
		"mul_div": "" +
			"cdq\ncqo\nmul r11\nmul rsi\ndiv rsi\nidiv ecx\nidiv r9\nneg rax\nneg cl\nneg byte ptr [rdi]\nneg word ptr [rsi]\n",
		"inc_dec": "" +
			"inc eax\ninc rax\ndec r8\ninc cl\ndec ch\ninc qword ptr [rdi]\n" +
			"inc dword ptr [rax+4]\ndec qword ptr [rsp+8]\ninc byte ptr [rdi]\ninc word ptr [rsi]\n",
		"bitcount": "" +
			"bsf eax, ecx\nbsf rcx, rdx\nbsr rax, rdi\nbsr rax, qword ptr [rdi]\n" +
			"lzcnt r9, r10\nlzcnt eax, edi\ntzcnt eax, eax\ntzcnt rcx, rax\n" +
			"popcnt rax, rax\npopcnt r8d, r9d\npopcnt rax, qword ptr [rbp-8]\n",
		"imul": "" +
			"imul rax, rcx\nimul rdx, qword ptr [rsi+8]\nimul rcx, rcx, 20\nimul eax, eax, 1000\nimul r9, r10, -8\n",
		"push_pop": "" +
			"push rbp\npush r15\npush rax\npop rdi\npop r8\npushfq\npopfq\n",
		"string_ops": "" +
			"cld\nrep movsb\nrep movsq\nrep stosb\nrep stosq\nrepe cmpsb\nrepne scasb\n" +
			"movsw\nstosw\ncmpsq\nlodsb\n",
		"indirect_branch": "" +
			"call rax\ncall r11\njmp rdx\njmp r9\nret\nleave\nsyscall\nud2\n",
		"sse_scalar": "" +
			"movq xmm0, rax\nmovq r9, xmm3\nmovq xmm2, xmm7\nmovd xmm1, ecx\nmovd r10d, xmm12\n" +
			"movsd xmm0, xmm1\nmovsd xmm3, qword ptr [rdi+8]\nmovsd qword ptr [rsp], xmm2\n" +
			"movss xmm1, dword ptr [rsi]\nmovss dword ptr [rdi], xmm0\n" +
			"addsd xmm1, xmm0\nsubss xmm9, xmm10\nmulsd xmm4, qword ptr [rbp-16]\ndivss xmm0, xmm15\n" +
			"sqrtsd xmm8, xmm8\nminsd xmm1, xmm2\nmaxss xmm3, xmm4\n" +
			"ucomisd xmm1, xmm0\ncomiss xmm2, xmm3\nucomiss xmm5, xmm6\n" +
			"movaps xmm1, xmm2\nmovapd xmm3, xmm4\nxorps xmm0, xmm0\nxorpd xmm7, xmm7\nandpd xmm1, xmm2\n",
		"sse_convert": "" +
			"cvtsi2sd xmm0, rax\ncvtsi2ss xmm1, ecx\ncvttsd2si rax, xmm0\ncvttss2si edx, xmm5\n" +
			"cvtsd2si r9, xmm1\ncvtss2sd xmm0, xmm1\ncvtsd2ss xmm2, xmm3\nroundsd xmm0, xmm1, 9\n",
		"sse_packed": "" +
			"movdqu xmm0, [r8]\nmovdqu [rdi], xmm2\nmovdqa xmm1, [rax]\nmovdqa [rbx+16], xmm5\n" +
			"pcmpeqb xmm0, xmm1\npcmpeqw xmm2, xmm3\npcmpeqd xmm9, xmm1\n" +
			"punpcklbw xmm1, xmm1\npunpcklwd xmm2, xmm2\npshufd xmm1, xmm2, 0\npshufd xmm5, xmm6, 27\n" +
			"pmovmskb eax, xmm0\npmovmskb r10d, xmm11\npor xmm0, xmm1\npand xmm2, xmm3\npxor xmm4, xmm4\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, _, err := x86_64.AssembleProgram(src, elf.TextVAddr)
			if err != nil {
				t.Fatalf("AssembleProgram: %v", err)
			}
			want := gnuAsX86Text(t, as, objcopy, src)
			if !bytes.Equal(got, want) {
				t.Fatalf("bytes differ from GNU as:\n got  % x\n want % x", got, want)
			}
		})
	}
}

// findX86Binutils locates an x86-64-targeting as + objcopy, skipping when
// they are missing or when the host `as` targets another architecture
// (verified by assembling a one-byte Intel-syntax probe).
func findX86Binutils(t *testing.T) (as, objcopy string) {
	t.Helper()
	var err error
	if as, err = exec.LookPath("as"); err != nil {
		t.Skip("as not on PATH")
	}
	if objcopy, err = exec.LookPath("objcopy"); err != nil {
		t.Skip("objcopy not on PATH")
	}
	dir := t.TempDir()
	sPath := filepath.Join(dir, "probe.s")
	if err := os.WriteFile(sPath, []byte(".intel_syntax noprefix\n.text\nret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(as, sPath, "-o", filepath.Join(dir, "probe.o")).Run(); err != nil {
		t.Skipf("as does not assemble x86-64 Intel syntax: %v", err)
	}
	return as, objcopy
}

// gnuAsX86Text assembles Intel-syntax src with GNU as and extracts the raw
// .text bytes.
func gnuAsX86Text(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	oPath := filepath.Join(dir, "in.o")
	binPath := filepath.Join(dir, "in.bin")
	full := ".intel_syntax noprefix\n.text\n" + src
	if err := os.WriteFile(sPath, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(as, sPath, "-o", oPath).CombinedOutput(); err != nil {
		t.Fatalf("as: %v\n%s", err, out)
	}
	if out, err := exec.Command(objcopy, "-O", "binary", "--only-section=.text", oPath, binPath).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
