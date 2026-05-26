package elf_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
)

// TestStaticExecutableHeader checks the fixed-layout fields of the
// produced ELF-64 header + program header without needing any tools:
// magic, class/data, machine = EM_AARCH64, one PT_LOAD, and an entry
// that points just past the two headers.
func TestStaticExecutableHeader(t *testing.T) {
	text := []byte{0x00, 0x00, 0x80, 0xd2} // one instruction (movz x0,#0)
	bin := elf.StaticExecutable(text)

	if len(bin) != 64+56+len(text) {
		t.Fatalf("len = %d, want %d", len(bin), 64+56+len(text))
	}
	if string(bin[:4]) != "\x7fELF" {
		t.Errorf("bad magic: % x", bin[:4])
	}
	if bin[4] != 2 || bin[5] != 1 { // ELFCLASS64, ELFDATA2LSB
		t.Errorf("class/data = %d/%d, want 2/1", bin[4], bin[5])
	}
	if e_type := u16(bin, 16); e_type != 2 { // ET_EXEC
		t.Errorf("e_type = %d, want 2 (ET_EXEC)", e_type)
	}
	if e_machine := u16(bin, 18); e_machine != 183 { // EM_AARCH64
		t.Errorf("e_machine = %d, want 183 (EM_AARCH64)", e_machine)
	}
	if e_phnum := u16(bin, 56); e_phnum != 1 {
		t.Errorf("e_phnum = %d, want 1", e_phnum)
	}
	if e_entry := u64(bin, 24); e_entry != 0x400000+64+56 {
		t.Errorf("e_entry = %#x, want %#x", e_entry, 0x400000+64+56)
	}
	// Program header begins at e_phoff = 64; p_type must be PT_LOAD(1).
	if p_type := u32(bin, 64); p_type != 1 {
		t.Errorf("p_type = %d, want 1 (PT_LOAD)", p_type)
	}
	if p_flags := u32(bin, 68); p_flags != 5 { // PF_R|PF_X
		t.Errorf("p_flags = %d, want 5 (R|X)", p_flags)
	}
}

// TestExitCodeRunsUnderQemu is the end-to-end gate: encode a tiny
// exit(42) program, wrap it in a static ELF via StaticExecutable,
// and run it under qemu-aarch64. The whole pipeline — instruction
// encoding, ELF layout, kernel/qemu load, syscall — has to be right
// for the process to exit 42.
func TestExitCodeRunsUnderQemu(t *testing.T) {
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		if qemu, err = exec.LookPath("qemu-aarch64-static"); err != nil {
			t.Skip("qemu-aarch64 not on PATH")
		}
	}

	// exit(42): movz x0,#42 ; movz x8,#93 (__NR_exit) ; svc #0.
	var text []byte
	text = arm64.Put(text, arm64.MOVZ(0, 42, 0))
	text = arm64.Put(text, arm64.MOVZ(8, 93, 0))
	text = arm64.Put(text, arm64.SVC(0))
	bin := elf.StaticExecutable(text)

	path := filepath.Join(t.TempDir(), "exit42")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	cmd := exec.Command(qemu, path)
	err = cmd.Run()
	if err == nil {
		t.Fatalf("process exited 0, want 42")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run failed (not an exit code): %v", err)
	}
	if ee.ExitCode() != 42 {
		t.Fatalf("exit code = %d, want 42", ee.ExitCode())
	}
}

// TestArithmeticRunsUnderQemu exercises the move + add/sub encoders
// end-to-end: compute (40 + 5) - 3 = 42 across registers, then exit
// with it. Covers MOVZ, ADDreg, SUBimm, and the exit syscall in one
// runnable binary.
func TestArithmeticRunsUnderQemu(t *testing.T) {
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		if qemu, err = exec.LookPath("qemu-aarch64-static"); err != nil {
			t.Skip("qemu-aarch64 not on PATH")
		}
	}

	// x1 = 40 ; x2 = 5 ; x1 = x1 + x2 (=45) ; x0 = x1 - 3 (=42) ;
	// x8 = 93 (__NR_exit) ; svc #0.
	var text []byte
	text = arm64.Put(text, arm64.MOVZ(1, 40, 0))
	text = arm64.Put(text, arm64.MOVZ(2, 5, 0))
	text = arm64.Put(text, arm64.ADDreg(1, 1, 2))
	text = arm64.Put(text, arm64.SUBimm(0, 1, 3, false))
	text = arm64.Put(text, arm64.MOVZ(8, 93, 0))
	text = arm64.Put(text, arm64.SVC(0))
	bin := elf.StaticExecutable(text)

	path := filepath.Join(t.TempDir(), "arith42")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	err = exec.Command(qemu, path).Run()
	if err == nil {
		t.Fatalf("process exited 0, want 42")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run failed (not an exit code): %v", err)
	}
	if ee.ExitCode() != 42 {
		t.Fatalf("exit code = %d, want 42", ee.ExitCode())
	}
}

// TestMulRunsUnderQemu exercises MUL end-to-end: 6 * 7 = 42, exit.
func TestMulRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		var c []byte
		c = arm64.Put(c, arm64.MOVZ(1, 7, 0))
		c = arm64.Put(c, arm64.MOVZ(2, 6, 0))
		c = arm64.Put(c, arm64.MUL(0, 1, 2)) // x0 = 7 * 6 = 42
		c = arm64.Put(c, arm64.MOVZ(8, 93, 0))
		return arm64.Put(c, arm64.SVC(0))
	})
}

// TestShiftRunsUnderQemu exercises the variable shift LSLV: 21 << 1 = 42.
func TestShiftRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		var c []byte
		c = arm64.Put(c, arm64.MOVZ(1, 21, 0))
		c = arm64.Put(c, arm64.MOVZ(2, 1, 0))
		c = arm64.Put(c, arm64.LSLV(0, 1, 2)) // x0 = 21 << 1 = 42
		c = arm64.Put(c, arm64.MOVZ(8, 93, 0))
		return arm64.Put(c, arm64.SVC(0))
	})
}

// TestStackRoundTripRunsUnderQemu exercises the frame + word load/store
// path: set up a frame (STP pre-index), store 42 to the stack, clobber
// the register, reload it (STR/LDR), tear the frame down (LDP
// post-index), and exit with the reloaded value.
func TestStackRoundTripRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		var c []byte
		c = arm64.Put(c, arm64.STPpre(29, 30, 31, -16)) // stp x29,x30,[sp,#-16]!
		c = arm64.Put(c, arm64.MOVZ(0, 42, 0))
		c = arm64.Put(c, arm64.STRimm(0, 31, 8))        // str x0, [sp, #8]
		c = arm64.Put(c, arm64.MOVZ(0, 0, 0))           // clobber x0
		c = arm64.Put(c, arm64.LDRimm(0, 31, 8))        // ldr x0, [sp, #8]
		c = arm64.Put(c, arm64.LDPpost(29, 30, 31, 16)) // ldp x29,x30,[sp],#16
		c = arm64.Put(c, arm64.MOVZ(8, 93, 0))
		return arm64.Put(c, arm64.SVC(0))
	})
}

// TestByteRoundTripRunsUnderQemu exercises STRB/LDRB: store the byte 42
// to the stack and read it back zero-extended, then exit with it.
func TestByteRoundTripRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		var c []byte
		c = arm64.Put(c, arm64.STPpre(29, 30, 31, -16))
		c = arm64.Put(c, arm64.MOVZ(0, 42, 0))
		c = arm64.Put(c, arm64.STRBimm(0, 31, 8)) // strb w0, [sp, #8]
		c = arm64.Put(c, arm64.MOVZ(0, 0, 0))
		c = arm64.Put(c, arm64.LDRBimm(0, 31, 8)) // ldrb w0, [sp, #8]
		c = arm64.Put(c, arm64.LDPpost(29, 30, 31, 16))
		c = arm64.Put(c, arm64.MOVZ(8, 93, 0))
		return arm64.Put(c, arm64.SVC(0))
	})
}

// TestLoopRunsUnderQemu is the control-flow gate: a countdown loop
// that increments an accumulator 42 times exercises CBZ (forward
// branch), B (backward branch), and the assembler's two-pass label
// resolution in both directions — then exits with the accumulator.
//
//	x0 = 0 (acc) ; x1 = 42 (counter)
//	loop: cbz x1, done ; x0++ ; x1-- ; b loop
//	done: exit(x0)
func TestLoopRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		a := arm64.NewAssembler()
		a.Emit(arm64.MOVZ(0, 0, 0))  // x0 = 0
		a.Emit(arm64.MOVZ(1, 42, 0)) // x1 = 42
		a.Label("loop")
		a.CBZ(1, "done")
		a.Emit(arm64.ADDimm(0, 0, 1, false)) // x0 += 1
		a.Emit(arm64.SUBimm(1, 1, 1, false)) // x1 -= 1
		a.B("loop")
		a.Label("done")
		a.Emit(arm64.MOVZ(8, 93, 0))
		a.Emit(arm64.SVC(0))
		code, err := a.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestCallRunsUnderQemu exercises BL/RET: main calls a subroutine
// that sets x0=42 and returns (via x30), then main exits with x0.
func TestCallRunsUnderQemu(t *testing.T) {
	runExpectExit(t, 42, func() []byte {
		a := arm64.NewAssembler()
		a.BL("setval")               // call setval (links return addr in x30)
		a.Emit(arm64.MOVZ(8, 93, 0)) // (on return) x8 = __NR_exit
		a.Emit(arm64.SVC(0))         // exit(x0)
		a.Label("setval")
		a.Emit(arm64.MOVZ(0, 42, 0)) // x0 = 42
		a.Emit(arm64.RET(30))        // return to x30
		code, err := a.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledTextRunsUnderQemu is the full text→bytes→ELF→run gate:
// a complete program written as GAS assembly text is assembled by
// arm64.Assemble, wrapped in an ELF, and run under qemu. The loop
// counts x0 up to 42 (cmp/b.eq forward, b backward), proving the
// text parser, encoders, label resolution, and ELF writer all line up.
func TestAssembledTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x0, #0\n" + // acc
		"\tmov x1, #42\n" + // counter
		"loop:\n" +
		"\tcmp x1, #0\n" +
		"\tb.eq done\n" +
		"\tadd x0, x0, #1\n" +
		"\tsub x1, x1, #1\n" +
		"\tb loop\n" +
		"done:\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledStackTextRunsUnderQemu assembles a stack-frame
// round-trip written as GAS text (frame setup, store to stack, clobber,
// reload, frame teardown) and runs it under qemu — exercising the
// memory-operand parsing (stp/str/ldr/ldp with brackets) end-to-end.
func TestAssembledStackTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tstp x29, x30, [sp, #-16]!\n" +
		"\tmov x0, #42\n" +
		"\tstr x0, [sp, #8]\n" +
		"\tmov x0, #0\n" +
		"\tldr x0, [sp, #8]\n" +
		"\tldp x29, x30, [sp], #16\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledShiftImmTextRunsUnderQemu exercises the immediate-shift
// alias end-to-end: 84 >> 1 = 42, assembled from text and run.
func TestAssembledShiftImmTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x0, #84\n" +
		"\tlsr x0, x0, #1\n" + // 84 >> 1 = 42
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledDivTextRunsUnderQemu exercises udiv end-to-end:
// 84 / 2 = 42, assembled from text and run.
func TestAssembledDivTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x1, #84\n" +
		"\tmov x2, #2\n" +
		"\tudiv x0, x1, x2\n" + // 84 / 2 = 42
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledUnscaledTextRunsUnderQemu exercises stur/ldur with a
// 4-byte (non-8-aligned) offset — a displacement only the unscaled
// form can encode — storing 42 into a reserved frame slot and reading
// it back.
func TestAssembledUnscaledTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tsub sp, sp, #16\n" +
		"\tmov x0, #42\n" +
		"\tstur x0, [sp, #4]\n" + // offset 4 is not 8-aligned -> needs stur
		"\tmov x0, #0\n" +
		"\tldur x0, [sp, #4]\n" +
		"\tadd sp, sp, #16\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledWRegisterTextRunsUnderQemu exercises the 32-bit ALU
// path: compute 42 entirely in w-registers (the sf-cleared encodings)
// and exit with it. The exit status reads the low byte of x0, which
// the w-register writes zero-extend into, so 42 propagates.
func TestAssembledWRegisterTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov w1, #40\n" +
		"\tmov w2, #2\n" +
		"\tadd w0, w1, w2\n" + // 40 + 2 = 42 (32-bit add)
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledFloatTextRunsUnderQemu exercises the double-precision
// path end-to-end: 84.0 / 2.0 = 42.0, converted back to an integer.
// Covers scvtf, fdiv, and fcvtzs in one runnable binary.
func TestAssembledFloatTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x1, #84\n" +
		"\tscvtf d0, x1\n" + // d0 = 84.0
		"\tmov x2, #2\n" +
		"\tscvtf d1, x2\n" + // d1 = 2.0
		"\tfdiv d2, d0, d1\n" + // d2 = 42.0
		"\tfcvtzs x0, d2\n" + // x0 = 42
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledSignedLoadTextRunsUnderQemu exercises ldrsb: store the
// byte 42 to the stack and sign-extend-load it into a 64-bit register,
// then exit with it.
func TestAssembledSignedLoadTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tsub sp, sp, #16\n" +
		"\tmov x0, #42\n" +
		"\tstrb w0, [sp, #0]\n" +
		"\tmov x0, #0\n" +
		"\tldrsb x0, [sp, #0]\n" + // sign-extend-load 42 (positive) -> 42
		"\tadd sp, sp, #16\n" +
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledSinglePrecisionTextRunsUnderQemu exercises the
// single-precision path + ucvtf/fcvtzu: 84.0f / 2.0f = 42, via
// unsigned int->double->single arithmetic and back.
func TestAssembledSinglePrecisionTextRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x1, #84\n" +
		"\tucvtf d0, x1\n" + // d0 = 84.0
		"\tfcvt s0, d0\n" + // s0 = 84.0f
		"\tmov x2, #2\n" +
		"\tucvtf d1, x2\n" +
		"\tfcvt s1, d1\n" + // s1 = 2.0f
		"\tfdiv s2, s0, s1\n" + // s2 = 42.0f
		"\tfcvt d2, s2\n" + // d2 = 42.0
		"\tfcvtzu x0, d2\n" + // x0 = 42
		"\tmov x8, #93\n" +
		"\tsvc #0\n"
	runExpectExit(t, 42, func() []byte {
		code, err := arm64.Assemble(src)
		if err != nil {
			t.Fatal(err)
		}
		return code
	})
}

// TestAssembledDataTextRunsUnderQemu is the symbol-addressing gate: a
// program materialises the address of a .rodata constant via adrp +
// add #:lo12:, loads the value (42), and exits with it. Wrong adrp page
// math or rodata layout would load garbage and miss exit 42.
func TestAssembledDataTextRunsUnderQemu(t *testing.T) {
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		if qemu, err = exec.LookPath("qemu-aarch64-static"); err != nil {
			t.Skip("qemu-aarch64 not on PATH")
		}
	}
	src := "" +
		"\t.text\n" +
		"\tadrp x1, val\n" +
		"\tadd x1, x1, :lo12:val\n" +
		"\tldr x0, [x1]\n" + // x0 = *val = 42
		"\tmov x8, #93\n" +
		"\tsvc #0\n" +
		"\t.section .rodata\n" +
		"\t.balign 8\n" +
		"val:\n" +
		"\t.8byte 42\n"
	text, rodata, err := arm64.AssembleProgram(src, elf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v", err)
	}
	bin := elf.StaticExecutableData(text, rodata)
	path := filepath.Join(t.TempDir(), "data42")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	err = exec.Command(qemu, path).Run()
	got := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed: %v", err)
		}
		got = ee.ExitCode()
	}
	if got != 42 {
		t.Fatalf("exit code = %d, want 42", got)
	}
}

// runExpectExit builds an ELF from the instructions returned by gen,
// runs it under qemu-aarch64, and asserts the process exit code.
func runExpectExit(t *testing.T, want int, gen func() []byte) {
	t.Helper()
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		if qemu, err = exec.LookPath("qemu-aarch64-static"); err != nil {
			t.Skip("qemu-aarch64 not on PATH")
		}
	}
	bin := elf.StaticExecutable(gen())
	path := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	err = exec.Command(qemu, path).Run()
	got := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run failed (not an exit code): %v", err)
		}
		got = ee.ExitCode()
	}
	if got != want {
		t.Fatalf("exit code = %d, want %d", got, want)
	}
}

func u16(b []byte, off int) uint16 {
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func u32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

func u64(b []byte, off int) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[off+i])
	}
	return v
}
