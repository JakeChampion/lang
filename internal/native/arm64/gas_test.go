package arm64_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// TestFcvtToIntDestWidth pins the destination-width handling of the
// float→int converts. The encoders are the sf=1 (x-register) forms;
// a `w` destination must clear bit 31 to select the sf=0 (32-bit)
// instruction, which saturates to the 32-bit range. Without that a
// `fcvtzs w0, d1` was wrongly assembled as the 64-bit conversion, so
// an out-of-range f→i32 cast saturated to the i64 limit. Single-
// precision sources (type=00) must also reach the right encoding.
// Known encodings (no external assembler needed):
//
//	fcvtzs w0, d1 = 0x1e780020   fcvtzs x0, d1 = 0x9e780020
//	fcvtzs w0, s1 = 0x1e380020   fcvtzu w0, d1 = 0x1e790020
//	fcvtzu w0, s1 = 0x1e390020   fcvtzu x0, s1 = 0x9e390020
func TestFcvtToIntDestWidth(t *testing.T) {
	cases := []struct {
		asm  string
		want uint32
	}{
		{"\tfcvtzs w0, d1\n", 0x1e780020},
		{"\tfcvtzs x0, d1\n", 0x9e780020},
		{"\tfcvtzs w0, s1\n", 0x1e380020},
		{"\tfcvtzu w0, d1\n", 0x1e790020},
		{"\tfcvtzu w0, s1\n", 0x1e390020},
		{"\tfcvtzu x0, s1\n", 0x9e390020},
		// int → float, both signedness and dest/source widths.
		// ucvtf needs the single-dest (type=00) + 32-bit-source
		// (sf=0) forms an unsigned `u64 as f32` / `u32 as f32`
		// reaches; without them the assembler errored / mis-encoded.
		{"\tucvtf s0, x1\n", 0x9e230020},
		{"\tucvtf s0, w1\n", 0x1e230020},
		{"\tucvtf d0, x1\n", 0x9e630020},
		{"\tucvtf d0, w1\n", 0x1e630020},
		{"\tscvtf s0, x1\n", 0x9e220020},
		{"\tscvtf s0, w1\n", 0x1e220020},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c.asm, err)
		}
		want := arm64.Put(nil, c.want)
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got % x, want % x", c.asm, got, want)
		}
	}
}

// TestCBZWidth pins the sf bit on cbz/cbnz to the operand register's
// width: a `w` operand is the 32-bit (sf=0) compare, an `x` operand the
// 64-bit (sf=1) compare — matching GNU as. A regression here silently
// compared the wrong number of bytes (the assembler had ignored the
// prefix and always emitted the 64-bit form), so a `cbz w0` testing a
// value with non-zero high bits would branch wrong vs an external
// toolchain. imm19 is +0 here (label on the next line is one ahead, so
// offset 1): keep the cases single-instruction by branching to a label
// right after, giving offset 1 (imm19=1 -> bits[23:5] = 0x20).
func TestCBZWidth(t *testing.T) {
	cases := []struct {
		asm  string
		want uint32
	}{
		{"\tcbz w0, .L\n.L:\n", 0x34000020},  // sf=0 (32-bit)
		{"\tcbz x0, .L\n.L:\n", 0xb4000020},  // sf=1 (64-bit)
		{"\tcbnz w0, .L\n.L:\n", 0x35000020}, // sf=0
		{"\tcbnz x0, .L\n.L:\n", 0xb5000020}, // sf=1
		{"\tcbz w3, .L\n.L:\n", 0x34000023},  // register field carries through
		{"\tcbz x3, .L\n.L:\n", 0xb4000023},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c.asm, err)
		}
		// The instruction is the first 4 bytes (the label adds none).
		want := arm64.Put(nil, c.want)
		if !bytes.Equal(got[:4], want) {
			t.Errorf("%q: got % x, want % x", c.asm, got[:4], want)
		}
	}
}

// TestSelfHostDialectEncodings pins the instruction forms the SELF-HOST
// arm64 emitter uses that the Go backend does not — the gap set found by
// assembling the stage-2 self-compile output (asm_arm64.fern's dialect):
// negative/unaligned load-store offsets (routed to the unscaled
// LDUR/STUR encodings), the uxtb/uxth zero-extends, the flag-setting
// adds/subs, `fcmp Dn, #0.0`, and the VFP-imm8 `fmov Dd, #imm`. Every
// expected word is what aarch64-linux-gnu-as + objdump produce for the
// same line.
func TestSelfHostDialectEncodings(t *testing.T) {
	cases := []struct {
		asm  string
		want uint32
	}{
		{"\tstr x0, [x29, #-8]\n", 0xf81f83a0},  // stur
		{"\tldr x1, [x29, #-16]\n", 0xf85f03a1}, // ldur
		{"\tstr w2, [x29, #-20]\n", 0xb81ec3a2},
		{"\tldr w3, [x29, #-4]\n", 0xb85fc3a3},
		{"\tstrb w4, [x29, #-1]\n", 0x381ff3a4},
		{"\tldrb w5, [x29, #-3]\n", 0x385fd3a5},
		{"\tstrh w6, [x29, #-2]\n", 0x781fe3a6},
		{"\tldrh w7, [x29, #-6]\n", 0x785fa3a7},
		{"\tuxtb w0, w0\n", 0x53001c00},
		{"\tuxth w1, w2\n", 0x53003c41},
		{"\tsubs x2, x2, #1\n", 0xf1000442},
		{"\tadds x3, x4, #7\n", 0xb1001c83},
		{"\tsubs x5, x6, x7\n", 0xeb0700c5},
		{"\tsubs w8, w9, #1\n", 0x71000528},
		{"\tfcmp d0, #0.0\n", 0x1e602008},
		{"\tfmov d1, #2.0\n", 0x1e601001},
		{"\tfmov d0, #1.0\n", 0x1e6e1000},
		{"\tfmov d2, #-4.5\n", 0x1e725002},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c.asm, err)
		}
		want := arm64.Put(nil, c.want)
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got % x, want % x", c.asm, got, want)
		}
	}
}

// TestAssembleBasic checks Assemble without external tools: a tiny
// exit(42) snippet must produce the known movz/movz/svc bytes, and
// labels + a comment must be handled.
func TestAssembleBasic(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\t.global _start\n" +
		"_start:\n" +
		"\tmov x0, #42   // exit status\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	got, err := arm64.Assemble(src)
	if err != nil {
		t.Fatal(err)
	}
	var want []byte
	want = arm64.Put(want, arm64.MOVZ(0, 42, 0))
	want = arm64.Put(want, arm64.MOVZ(8, 93, 0))
	want = arm64.Put(want, arm64.SVC(0))
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestAssembleErrors(t *testing.T) {
	if _, err := arm64.Assemble("\tfjcvtzs x0, d1\n"); err == nil {
		t.Error("expected error for unsupported instruction")
	}
	if _, err := arm64.Assemble("\t.quad 5\n"); err == nil {
		t.Error("expected error for unsupported directive")
	}
	if _, err := arm64.Assemble("\tb nowhere\n"); err == nil {
		t.Error("expected error for undefined label")
	}
}

// TestAssembleAgainstGNUAs is the byte-exact oracle: each snippet is
// assembled both by Assemble and by aarch64-linux-gnu-as, and the
// .text bytes must match. This pins the whole parser+encoder stack to
// an independent reference.
func TestAssembleAgainstGNUAs(t *testing.T) {
	as, objcopy := findBinutils(t)

	cases := map[string]string{
		"moves": "" +
			"\tmov x0, #42\n\tmov x1, x0\n\tmovz x8, #93\n\tmovk x3, #0xabcd\n",
		"arith": "" +
			"\tadd x0, x1, #1\n\tadd x2, x3, #1, lsl #12\n\tsub x4, x5, x6\n\tadd x0, x1, x2\n",
		"logical_mul_shift": "" +
			"\tand x0, x1, x2\n\torr x0, x1, x2\n\teor x0, x1, x2\n\tmul x0, x1, x2\n\tlsl x0, x1, x2\n\tlsr x0, x1, x2\n\tasr x0, x1, x2\n",
		"bitfield_extract": "" +
			"\tubfx x1, x1, #56, #4\n\tubfx x0, x2, #0, #8\n\tsbfx x3, x4, #8, #16\n",
		"shift_imm_and_extend": "" +
			"\tlsl x0, x1, #4\n\tlsr x0, x1, #4\n\tasr x0, x1, #4\n\tlsl x2, x3, #1\n\tsxtb x0, w1\n\tsxth x0, w1\n\tsxtw x0, w1\n",
		"compare": "" +
			"\tcmp x1, x2\n\tcmp x1, #5\n",
		"w_register_alu": "" +
			"\tmov w0, #42\n\tmov w1, w0\n\tmovz w8, #93\n\tadd w0, w1, #1\n\tadd w0, w1, w2\n\tsub w4, w5, w6\n" +
			"\tand w0, w1, w2\n\tmul w0, w1, w2\n\tcmp w1, w2\n\tcmp w1, #5\n\tcsel w0, w1, w2, eq\n\tcset w0, ne\n" +
			"\tudiv w0, w1, w2\n\tlsl w0, w1, w2\n\tneg w0, w1\n",
		"float_double": "" +
			"\tfadd d0, d1, d2\n\tfsub d0, d1, d2\n\tfmul d0, d1, d2\n\tfdiv d0, d1, d2\n\tfneg d0, d1\n\tfcmp d1, d2\n" +
			"\tfmov d0, d1\n\tfmov d0, x1\n\tfmov x0, d1\n\tscvtf d0, x1\n\tfcvtzs x0, d1\n",
		"float_single_converts": "" +
			"\tfadd s0, s1, s2\n\tfsub s0, s1, s2\n\tfmul s0, s1, s2\n\tfdiv s0, s1, s2\n\tfneg s0, s1\n\tfcmp s1, s2\n\tfmov s0, s1\n" +
			"\tfcvt d0, s1\n\tfcvt s0, d1\n\tscvtf s0, x1\n\tfcvtzs x0, s1\n\tucvtf d0, x1\n\tfcvtzu x0, d1\n\tfmov s0, w1\n\tfmov w0, s1\n",
		"csel_div_extras": "" +
			"\tcsel x0, x1, x2, eq\n\tcsel x3, x4, x5, lt\n\tcset x0, ne\n\tcset x7, ge\n" +
			"\tcmn x1, x2\n\tneg x0, x1\n\tudiv x0, x1, x2\n\tsdiv x0, x1, x2\n\tmsub x0, x1, x2, x3\n",
		"memory": "" +
			"\tldr x0, [x1]\n\tldr x0, [x1, #16]\n\tstr x2, [x3, #8]\n" +
			"\tldrb w4, [x5, #1]\n\tstrb w6, [x7, #2]\n\tldrh w0, [x1, #4]\n\tstrh w2, [x3, #6]\n",
		"frame_pair": "" +
			"\tstp x29, x30, [sp, #-16]!\n\tldp x29, x30, [sp], #16\n",
		"indexed_and_mov_sp": "" +
			"\tstr x0, [sp, #-16]!\n\tldr x0, [sp], #16\n\tstr x1, [x2, #8]!\n\tldr x3, [x4], #-8\n\tmov sp, x29\n\tmov x0, sp\n",
		"signed_loads": "" +
			"\tldrsb x0, [x1]\n\tldrsb w0, [x1, #1]\n\tldrsh x0, [x1, #2]\n\tldrsh w0, [x1, #2]\n\tldrsw x0, [x1, #4]\n",
		"word32_ldst": "" +
			"\tldr w0, [x1]\n\tldr w2, [x3, #8]\n\tstr w0, [x1, #4]\n\tldr w0, [x1, x2, lsl #2]\n\tldr w0, [x1, x2]\n" +
			"\tstr w3, [x4], #4\n\tldr w5, [x6, #4]!\n\tldur w2, [x0, #-8]\n\tstur w1, [x0, #-8]\n",
		"register_offset": "" +
			"\tldr x3, [x2, x1, lsl #3]\n\tldr x0, [x1, x2]\n\tstr x3, [x2, x1, lsl #3]\n",
		"cmn_imm": "" +
			"\tcmn x0, #0\n\tcmn x1, #5\n\tcmn w2, #5\n",
		"shift_imm_w": "" +
			"\tlsl w1, w1, #31\n\tlsr w0, w1, #4\n\tasr w0, w1, #4\n\tlsl w2, w3, #1\n",
		"ldst_indexed": "" +
			"\tldrb w4, [x1], #1\n\tstrb w4, [x1], #1\n\tldrb w0, [x2, #1]!\n\tldrh w0, [x1], #2\n\tstrh w0, [x1, #2]!\n",
		"pair_modes": "" +
			"\tstp x19, x20, [sp, #16]\n\tldp x19, x20, [sp, #16]\n\tstp x29, x30, [sp]\n",
		"mov_immediate": "" +
			"\tmov x4, #-1\n\tmov x0, #-16\n\tmov x1, #-256\n\tmov w2, #-1\n\tmov x0, #65536\n",
		"unscaled": "" +
			"\tldur x0, [x1, #-8]\n\tstur x0, [x1, #-8]\n\tldur x2, [x3, #15]\n\tstur x4, [x5]\n\tldurb w0, [x1, #-1]\n\tsturb w0, [x1, #-1]\n",
		"branch_regs": "" +
			"\tbr x0\n\tblr x1\n\tret\n\tsvc #0\n",
		"test_branches": "" +
			"\tmov x0, #1\nlt0:\n\ttbz x0, #0, lt1\n\ttbnz x1, #63, lt0\n\ttbz w2, #5, lt1\nlt1:\n\tret\n",
		"labels_and_branches": "" +
			"loop:\n\tcmp x0, #0\n\tb.eq done\n\tsub x0, x0, #1\n\tcbnz x0, loop\n\tb loop\ndone:\n\tbeq loop\n\tret\n",
		"expr_immediates": "" +
			"\tldr x23, [x29, #96 + 48]\n\tstr x0, [x29, #16 + 8]\n\tadd x0, x1, #8 + 4\n\tsub x2, x3, #32 - 16\n\tmov x0, #16 + 16\n",
		"logical_shifted_reg": "" +
			"\torr w3, w1, w1, lsl #8\n\torr x3, x1, x2, lsl #16\n\tand w0, w1, w2, lsl #4\n\teor x0, x1, x2, lsr #1\n" +
			"\tand x5, x6, x7, asr #2\n\teor w0, w1, w2, ror #3\n",
		"rev16": "" +
			"\trev16 w0, w19\n\trev16 w5, w6\n\trev16 x2, x3\n",
		"narrow_reg_offset": "" +
			"\tldrb w0, [x22, x20]\n\tstrb w1, [x2, x3]\n\tldrh w0, [x1, x2]\n\tldrh w0, [x1, x2, lsl #1]\n\tstrh w4, [x5, x6]\n",
		"float_unary_intrinsics": "" +
			"\tfabs d0, d1\n\tfsqrt d0, d1\n\tfrintm d0, d1\n\tfrintp d0, d1\n\tfrintz d0, d1\n\tfrinta d0, d1\n",
		"fp_load_store": "" +
			"\tldr d1, [x12]\n\tldr d0, [sp, #8]\n\tstr d0, [sp, #8]\n\tldr d8, [sp], #16\n\tstr d8, [sp, #-16]!\n",
		"addsub_shifted_extended": "" +
			"\tadd x0, x1, x0, lsl #3\n\tadd x5, x6, x7, lsl #2\n\tsub x0, x1, x2, lsl #1\n\tadd w0, w1, w2, lsl #2\n" +
			"\tadd x2, x0, w1, uxtw\n\tadd x2, x0, w1, uxtw #2\n\tadd x5, x6, w7, sxtw\n\tsub x2, x0, w1, uxtw #3\n",
		"addsub_cmp_large_imm": "" +
			"\tcmp x0, #0x10000\n\tcmp x19, #0x10000\n\tcmn x1, #0x2000\n" +
			"\tadd x0, x1, #0x10000\n\tsub x2, x3, #0x1000\n\tadd x0, x1, #1, lsl #12\n",
		"scvtf_w_and_mov_zr": "" +
			"\tscvtf d0, w0\n\tscvtf s0, w1\n\tscvtf d2, x3\n\tmov x0, xzr\n\tmov w1, wzr\n",
		// w-form ALU ops not otherwise cross-checked, plus cbz/cbnz in
		// both widths. The sf bit (bit 31) selects 32- vs 64-bit; a
		// regression that ignores the register width (as cbz/cbnz once
		// did — it always emitted the 64-bit form) is caught here against
		// GNU as as the authority.
		"w_register_alu_extras": "" +
			"\torr w0, w1, w2\n\teor w0, w1, w2\n\tsdiv w0, w1, w2\n\tlsr w0, w1, w2\n\tasr w0, w1, w2\n" +
			"\tmsub w0, w1, w2, w3\n\tlsr w0, w1, #3\n\tasr w0, w1, #3\n",
		"cbz_cbnz_widths": "" +
			"\tcbz w0, l0\n\tcbz x0, l0\n\tcbnz w1, l0\n\tcbnz x1, l0\n\tcbz w3, l0\nl0:\n\tret\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := arm64.Assemble(src)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			want := gnuAsText(t, as, objcopy, src)
			if !bytes.Equal(got, want) {
				t.Fatalf("bytes differ from aarch64-linux-gnu-as:\n got  % x\n want % x", got, want)
			}
		})
	}
}

// TestLogicalImmAgainstGNUAs validates the bitmask-immediate encoder
// across a spread of patterns (runs of ones, rotations, replicated
// elements, and the negative #-16 the backend emits for alignment) by
// assembling `and/orr/eor x0, x0, #v` both with Assemble and with
// aarch64-linux-gnu-as and comparing the bytes.
func TestLogicalImmAgainstGNUAs(t *testing.T) {
	as, objcopy := findBinutils(t)
	vals := []int64{0xf, 0xff, 0xf0, -16, 0x3, 0x7, 0xffff, 0xfffff,
		0x5555555555555555, -6148914691236517206 /* 0xaaaa... */, 0x6, 1, -2}
	for _, op := range []string{"and", "orr", "eor"} {
		for _, v := range vals {
			src := fmt.Sprintf("\t%s x0, x0, #%d\n", op, v)
			got, err := arm64.Assemble(src)
			if err != nil {
				t.Errorf("%s #%d: Assemble: %v", op, v, err)
				continue
			}
			want := gnuAsText(t, as, objcopy, ".text\n"+src)
			if !bytes.Equal(got, want) {
				t.Errorf("%s #%d: got % x, want % x", op, v, got, want)
			}
		}
	}
}

func findBinutils(t *testing.T) (as, objcopy string) {
	t.Helper()
	var err error
	if as, err = exec.LookPath("aarch64-linux-gnu-as"); err != nil {
		t.Skip("aarch64-linux-gnu-as not on PATH")
	}
	if objcopy, err = exec.LookPath("aarch64-linux-gnu-objcopy"); err != nil {
		t.Skip("aarch64-linux-gnu-objcopy not on PATH")
	}
	return as, objcopy
}

// gnuAsText assembles src with GNU as and extracts the raw .text bytes.
func gnuAsText(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	dir := t.TempDir()
	sPath := filepath.Join(dir, "in.s")
	oPath := filepath.Join(dir, "in.o")
	binPath := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(sPath, []byte(src), 0o644); err != nil {
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

// TestAddSubSPRegister pins `add/sub sp, sp, <reg>` to the EXTENDED-register
// form. Register number 31 means SP only in the extended-register encoding;
// the shifted-register form (ADDreg/SUBreg) treats 31 as XZR, so a
// `sub sp, sp, x16` assembled as shifted-register becomes `sub xzr, xzr, x16`
// (`neg xzr, x16`) — a no-op that never moves SP. Large stack frames
// (> 4095 bytes) emit exactly this register-form frame adjust, so the bug left
// the frame unallocated and the operand stack overran the locals (issue #3598).
// Known encodings (extended-register, UXTX #0):
//
//	sub sp, sp, x16 = 0xcb3063ff   add sp, sp, x16 = 0x8b3063ff
//	sub sp, sp, x9  = 0xcb2963ff   add sp, x0, x1  = 0x8b21601f (Rn=x0, Rd=sp)
//
// A non-SP `sub x0, x0, x1` must stay the shifted-register form (0xcb010000).
func TestAddSubSPRegister(t *testing.T) {
	cases := []struct {
		asm  string
		want uint32
	}{
		{"\tsub sp, sp, x16\n", 0xcb3063ff},
		{"\tadd sp, sp, x16\n", 0x8b3063ff},
		{"\tsub sp, sp, x9\n", 0xcb2963ff},
		{"\tadd sp, x0, x1\n", 0x8b21601f},
		// Non-SP operands keep the shifted-register form.
		{"\tsub x0, x0, x1\n", 0xcb010000},
		{"\tadd x0, x0, x1\n", 0x8b010000},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c.asm, err)
		}
		want := arm64.Put(nil, c.want)
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got % x, want % x", c.asm, got, want)
		}
	}
}

// TestMovImmMovzMovk pins the movz/movk synthesis for immediates that fit
// neither a single movz/movn nor a bitmask: `mov x0, #70000` (two 16-bit lanes)
// must assemble to movz x0,#0x1170 ; movk x0,#1,lsl 16, and a value with a lane
// gap keeps the movz on its lowest non-zero lane.
func TestMovImmMovzMovk(t *testing.T) {
	cases := []struct {
		asm  string
		want []uint32
	}{
		// 70000 = 0x00011170 -> lanes 0x1170 (sh 0), 0x0001 (sh 16).
		{"\tmov x0, #70000\n", []uint32{arm64.MOVZ(0, 0x1170, 0), arm64.MOVK(0, 1, 16)}},
		// 1000000 = 0x000F4240 -> lanes 0x4240 (sh 0), 0x000F (sh 16).
		{"\tmov x1, #1000000\n", []uint32{arm64.MOVZ(1, 0x4240, 0), arm64.MOVK(1, 0xF, 16)}},
		// 0x0001_0000_1234 -> lanes 0x1234 (sh 0), 0x0001 (sh 32); the sh-16 zero
		// lane is skipped, so movk targets the high lane directly. (Not a bitmask
		// pattern, so it reaches the movz/movk fallback.)
		{"\tmov x2, #4294971956\n", []uint32{arm64.MOVZ(2, 0x1234, 0), arm64.MOVK(2, 1, 32)}},
	}
	for _, c := range cases {
		got, err := arm64.Assemble(c.asm)
		if err != nil {
			t.Fatalf("Assemble(%q): %v", c.asm, err)
		}
		var want []byte
		for _, w := range c.want {
			want = arm64.Put(want, w)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%q: got % x, want % x", c.asm, got, want)
		}
	}
}
