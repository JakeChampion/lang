package elf_test

import (
	"bytes"
	"debug/dwarf"
	goelf "debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
)

// TestWXPageAlignPerArch pins the per-architecture page alignment of the W^X
// two-segment image (#4380/#4382): x86-64 aligns its data segment to 4 KiB
// (its only page size), arm64 to 64 KiB (the max-page floor that loads on
// 4/16/64 KiB-page kernels). This is where a tiny CLI's size comes from — the
// pad between .text and the page-aligned data segment — so it also asserts the
// x86 image of a trivial program is far smaller than the old 64 KiB floor while
// arm64 stays at it.
func TestWXPageAlignPerArch(t *testing.T) {
	text := []byte{0xb8, 0x2a, 0x00, 0x00, 0x00, 0xc3} // tiny .text
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}             // one 8-byte slot

	binX := elf.StaticExecutableDataX86WX(text, data)
	binA := elf.StaticExecutableDataWX(text, data)

	dataAlign := func(bin []byte, arch string) uint64 {
		f, err := goelf.NewFile(bytes.NewReader(bin))
		if err != nil {
			t.Fatalf("%s: not a parseable ELF: %v", arch, err)
		}
		var last uint64
		for _, p := range f.Progs {
			if p.Type == goelf.PT_LOAD {
				last = p.Align
				// The R+W data segment must be congruent to its file offset
				// mod its alignment — what mmap requires.
				if p.Flags&goelf.PF_W != 0 && p.Off%p.Align != p.Vaddr%p.Align {
					t.Errorf("%s: data seg off %#x not congruent to vaddr %#x mod %#x", arch, p.Off, p.Vaddr, p.Align)
				}
			}
		}
		return last
	}

	if a := dataAlign(binX, "x86-64"); a != 0x1000 {
		t.Errorf("x86-64 W^X p_align = %#x, want 0x1000 (4 KiB)", a)
	}
	if a := dataAlign(binA, "arm64"); a != 0x10000 {
		t.Errorf("arm64 W^X p_align = %#x, want 0x10000 (64 KiB)", a)
	}
	// The x86 image no longer pays the 64 KiB floor; arm64 still does.
	if len(binX) >= 0x10000 {
		t.Errorf("x86-64 tiny W^X image = %d bytes, want < 64 KiB (page-align floor should be gone)", len(binX))
	}
	if len(binA) < 0x10000 {
		t.Errorf("arm64 tiny W^X image = %d bytes, want >= 64 KiB (still on the 64 KiB floor)", len(binA))
	}
}

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

// TestStaticExecutableDataWXHeader checks the W^X two-segment layout:
// two PT_LOAD program headers (R+X code, R+W data), an entry just past
// the two headers, and a data segment whose file offset and load address
// both land on the first 16 KiB page boundary past .text (so the segment
// is never both writable and executable, and the offset is congruent to
// the vaddr mod the page size).
func TestStaticExecutableDataWXHeader(t *testing.T) {
	text := []byte{0x00, 0x00, 0x80, 0xd2} // movz x0,#0
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8} // one 8-byte datum
	bin := elf.StaticExecutableDataWX(text, data)

	const base = 0x400000
	const headers = 64 + 2*56 // ehdr + two phdrs = 176
	const page = 0x10000
	dataOff := (uint64(headers+len(text)) + page - 1) &^ (page - 1)

	if want := int(dataOff) + len(data); len(bin) != want {
		t.Fatalf("len = %d, want %d", len(bin), want)
	}
	if e_type := u16(bin, 16); e_type != 2 { // ET_EXEC
		t.Errorf("e_type = %d, want 2 (ET_EXEC)", e_type)
	}
	if e_phnum := u16(bin, 56); e_phnum != 2 {
		t.Errorf("e_phnum = %d, want 2", e_phnum)
	}
	if e_entry := u64(bin, 24); e_entry != base+headers {
		t.Errorf("e_entry = %#x, want %#x", e_entry, base+headers)
	}

	// Program header 0 (offset 64): R+X code covering headers + .text.
	if p_type := u32(bin, 64); p_type != 1 {
		t.Errorf("seg0 p_type = %d, want 1 (PT_LOAD)", p_type)
	}
	if p_flags := u32(bin, 68); p_flags != 5 { // PF_R|PF_X
		t.Errorf("seg0 p_flags = %d, want 5 (R|X)", p_flags)
	}
	if p_offset := u64(bin, 72); p_offset != 0 {
		t.Errorf("seg0 p_offset = %d, want 0", p_offset)
	}
	if p_filesz := u64(bin, 96); p_filesz != uint64(headers+len(text)) {
		t.Errorf("seg0 p_filesz = %d, want %d", p_filesz, headers+len(text))
	}

	// Program header 1 (offset 120): R+W data on its own page.
	if p_type := u32(bin, 120); p_type != 1 {
		t.Errorf("seg1 p_type = %d, want 1 (PT_LOAD)", p_type)
	}
	if p_flags := u32(bin, 124); p_flags != 6 { // PF_R|PF_W
		t.Errorf("seg1 p_flags = %d, want 6 (R|W)", p_flags)
	}
	if p_offset := u64(bin, 128); p_offset != dataOff {
		t.Errorf("seg1 p_offset = %#x, want %#x", p_offset, dataOff)
	}
	if p_vaddr := u64(bin, 136); p_vaddr != base+dataOff {
		t.Errorf("seg1 p_vaddr = %#x, want %#x", p_vaddr, base+dataOff)
	}
	if p_filesz := u64(bin, 152); p_filesz != uint64(len(data)) {
		t.Errorf("seg1 p_filesz = %d, want %d", p_filesz, len(data))
	}
	// The two segments must not share a page (else one protection wins).
	codeEndPage := (uint64(headers+len(text)) - 1) / page
	if dataStartPage := dataOff / page; dataStartPage <= codeEndPage {
		t.Errorf("data page %d overlaps code end page %d", dataStartPage, codeEndPage)
	}
	// seg1 p_memsz (offset 160) covers the whole blob; with no trailing zeros it
	// equals p_filesz.
	if p_memsz := u64(bin, 160); p_memsz != uint64(len(data)) {
		t.Errorf("seg1 p_memsz = %d, want %d", p_memsz, len(data))
	}
}

// TestStaticExecutableDataWXBssNobits pins the .bss NOBITS optimisation: a data
// blob whose tail is zero-filled (the bump heap / strbuf / args globals the
// assembler materialises as trailing zeros) is stored in the file only up to its
// last non-zero byte — p_filesz — while p_memsz still spans the whole blob so the
// loader zero-fills the rest. This is what keeps arm64-ssa binaries from carrying
// their (potentially huge) zero-init regions on disk.
func TestStaticExecutableDataWXBssNobits(t *testing.T) {
	text := []byte{0x00, 0x00, 0x80, 0xd2} // movz x0,#0
	const initLen = 5
	const zeroTail = 4096
	data := make([]byte, initLen+zeroTail)
	for i := 0; i < initLen; i++ {
		data[i] = byte(i + 1) // 1..5, then a long zero tail (the "bss")
	}
	bin := elf.StaticExecutableDataWX(text, data)

	const page = 0x10000
	const headers = 64 + 2*56
	dataOff := (uint64(headers+len(text)) + page - 1) &^ (page - 1)

	// The file ends at the initialised prefix — the zero tail is NOT stored.
	if want := int(dataOff) + initLen; len(bin) != want {
		t.Fatalf("len(bin) = %d, want %d (zero tail must not be materialised)", len(bin), want)
	}
	if p_filesz := u64(bin, 152); p_filesz != initLen {
		t.Errorf("seg1 p_filesz = %d, want %d (initialised prefix only)", p_filesz, initLen)
	}
	if p_memsz := u64(bin, 160); p_memsz != uint64(len(data)) {
		t.Errorf("seg1 p_memsz = %d, want %d (full segment incl. zero-fill)", p_memsz, len(data))
	}
}

// TestAssembledDataWXRunsUnderQemu is the W^X symbol-addressing gate: the
// same adrp + add #:lo12: rodata load as TestAssembledDataTextRunsUnderQemu,
// but assembled with AssembleProgramWX and wrapped with
// StaticExecutableDataWX (page-aligned data in a separate R+W segment).
// Proves the page-aligned data resolution and two-segment load agree:
// loading the .rodata constant 42 and exiting with it.
func TestAssembledDataWXRunsUnderQemu(t *testing.T) {
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
	text, rodata, err := arm64.AssembleProgramWX(src, elf.TextVAddrWX)
	if err != nil {
		t.Fatalf("AssembleProgramWX: %v", err)
	}
	bin := elf.StaticExecutableDataWX(text, rodata)
	path := filepath.Join(t.TempDir(), "data42wx")
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

// TestStaticPieExecutableLayout checks the static-PIE (ET_DYN) image: a
// position-independent type, three program headers (R+X code, R+W data,
// PT_DYNAMIC) none of them W+X, one R_AARCH64_RELATIVE entry in .rela.dyn
// with base-relative offset/addend, and a .dynamic section whose DT_RELA*
// tags describe it. All addresses are relative to a load base of 0.
func TestStaticPieExecutableLayout(t *testing.T) {
	text := []byte{0x00, 0x00, 0x80, 0xd2} // movz x0,#0
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8} // one 8-byte slot
	// One relocation: slot at data start, target somewhere in .text.
	const headers = 64 + 3*56 // 232
	const page = 0x10000
	dataOff := (uint64(headers+len(text)) + page - 1) &^ (page - 1)
	relocs := []elf.Reloc{{Offset: dataOff, Addend: headers /* = TextVAddrPIE */}}
	bin := elf.StaticPieExecutable(text, data, relocs)

	if e_type := u16(bin, 16); e_type != 3 { // ET_DYN
		t.Errorf("e_type = %d, want 3 (ET_DYN)", e_type)
	}
	if e_entry := u64(bin, 24); e_entry != headers {
		t.Errorf("e_entry = %#x, want %#x (base-relative)", e_entry, headers)
	}
	if e_phnum := u16(bin, 56); e_phnum != 3 {
		t.Errorf("e_phnum = %d, want 3", e_phnum)
	}
	// PH2 (offset 64 + 2*56 = 176) is PT_DYNAMIC (p_type = 2).
	if p_type := u32(bin, 176); p_type != 2 {
		t.Errorf("PH2 p_type = %d, want 2 (PT_DYNAMIC)", p_type)
	}
	// Parse it with debug/elf as a sanity check that the dynamic image is
	// well-formed, and confirm the two PT_LOAD segments and no W+X.
	f, err := goelf.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	if f.Type != goelf.ET_DYN {
		t.Errorf("debug/elf type = %v, want ET_DYN", f.Type)
	}
	loads, dyn := 0, false
	for _, p := range f.Progs {
		switch p.Type {
		case goelf.PT_LOAD:
			loads++
			if p.Flags&goelf.PF_W != 0 && p.Flags&goelf.PF_X != 0 {
				t.Errorf("PT_LOAD is W+X (%v)", p.Flags)
			}
		case goelf.PT_DYNAMIC:
			dyn = true
		}
	}
	if loads != 2 || !dyn {
		t.Errorf("segments: %d PT_LOAD, PT_DYNAMIC=%v; want 2 + true", loads, dyn)
	}

	// .rela.dyn sits 8-aligned after the data blob: r_offset, r_info
	// (type = R_AARCH64_RELATIVE = 1027), r_addend.
	relaOff := dataOff + uint64(len(data)) // already 8-aligned here
	if got := u64(bin, int(relaOff)); got != dataOff {
		t.Errorf("rela r_offset = %#x, want %#x", got, dataOff)
	}
	if got := u64(bin, int(relaOff)+8); got != 1027 {
		t.Errorf("rela r_info = %d, want 1027 (R_AARCH64_RELATIVE)", got)
	}
	if got := u64(bin, int(relaOff)+16); got != headers {
		t.Errorf("rela r_addend = %#x, want %#x", got, headers)
	}
	// .dynamic follows: first tag DT_RELA(7) -> relaOff.
	dynOff := relaOff + 24
	if tag := u64(bin, int(dynOff)); tag != 7 {
		t.Errorf("first .dynamic tag = %d, want 7 (DT_RELA)", tag)
	}
	if val := u64(bin, int(dynOff)+8); val != relaOff {
		t.Errorf("DT_RELA value = %#x, want %#x", val, relaOff)
	}

	// The x86-64 PIE container shares the layout but differs in e_machine
	// (EM_X86_64) and relocation type (R_X86_64_RELATIVE = 8); exercised
	// end-to-end by e2e's TestX86_64NativePIESelfReloc, and at the byte
	// level here.
	// x86-64 pages are 4 KiB, so its data segment (and thus .rela.dyn) sits at
	// a 4 KiB — not 64 KiB — boundary past .text (#4380/#4382): recompute the
	// offset with the x86 page size rather than reusing the arm64 relaOff.
	const pageX = 0x1000
	dataOffX := (uint64(headers+len(text)) + pageX - 1) &^ (pageX - 1)
	relaOffX := dataOffX + uint64(len(data)) // already 8-aligned here
	binX := elf.StaticPieExecutableX86(text, data, relocs)
	if e_machine := u16(binX, 18); e_machine != 62 { // EM_X86_64
		t.Errorf("x86 e_machine = %d, want 62", e_machine)
	}
	if e_type := u16(binX, 16); e_type != 3 { // ET_DYN
		t.Errorf("x86 e_type = %d, want 3 (ET_DYN)", e_type)
	}
	if got := u64(binX, int(relaOffX)+8); got != 8 { // r_info type
		t.Errorf("x86 rela r_info = %d, want 8 (R_X86_64_RELATIVE)", got)
	}
}

// TestSharedLibraryX86Dlopen is the end-to-end gate for the .so emitter:
// build an x86-64 shared object exporting a C-ABI function `fern_answer`
// (mov eax, 42; ret), then dlopen + dlsym + call it from a gcc-compiled
// loader on the host. The exit code (42) proves the loader accepts the
// ET_DYN, resolves the export from .dynsym/.hash, and the code runs at the
// loader-chosen base — the prerequisite for JNI / System.loadLibrary.
func TestSharedLibraryX86Dlopen(t *testing.T) {
	gcc, err := exec.LookPath("gcc")
	if err != nil {
		t.Skip("gcc not on PATH; skipping .so dlopen test")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("host is not amd64; the x86-64 .so can't be dlopen'd natively")
	}
	// fern_answer: mov eax, 42 ; ret  (position-independent, no relocations).
	text := []byte{0xb8, 0x2a, 0x00, 0x00, 0x00, 0xc3}
	// .text loads at file offset = ELF header + 3 program headers = 232, and
	// the R+X segment has p_vaddr 0, so the function's base-relative address
	// is 232.
	const headers = 64 + 3*56
	exports := []elf.Export{{Name: "fern_answer", Value: headers, Size: uint64(len(text))}}
	so := elf.SharedLibraryX86(text, nil, nil, exports, "libfern.so")

	dir := t.TempDir()
	soPath := filepath.Join(dir, "libfern.so")
	if err := os.WriteFile(soPath, so, 0o755); err != nil {
		t.Fatal(err)
	}
	loader := `#include <dlfcn.h>
#include <stdio.h>
int main(int argc, char** argv) {
    void* h = dlopen(argv[1], RTLD_NOW);
    if (!h) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 2; }
    int (*f)(void) = (int(*)(void)) dlsym(h, "fern_answer");
    if (!f) { fprintf(stderr, "dlsym: %s\n", dlerror()); return 3; }
    return f();
}`
	cPath := filepath.Join(dir, "loader.c")
	if err := os.WriteFile(cPath, []byte(loader), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "loader")
	out, err := exec.Command(gcc, cPath, "-ldl", "-o", binPath).CombinedOutput()
	if err != nil {
		t.Fatalf("gcc loader: %v\n%s", err, out)
	}
	cmd := exec.Command(binPath, soPath)
	out, _ = cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Fatalf("dlopen+call fern_answer = %d, want 42 (out=%q)", code, out)
	}
}

// TestSharedLibraryArm64Structure checks the arm64 .so shares the same
// loadable structure (ET_DYN, three program headers incl. PT_DYNAMIC) and
// records the export name in .dynstr. The x86 dlopen test above exercises
// the format end-to-end; the emitter is machine-generic apart from
// e_machine, so this confirms the arm64 variant produces the same shape.
func TestSharedLibraryArm64Structure(t *testing.T) {
	text := []byte{0x40, 0x05, 0x80, 0xd2, 0xc0, 0x03, 0x5f, 0xd6} // mov x0,#42 ; ret
	const headers = 64 + 3*56
	so := elf.SharedLibrary(text, nil, nil,
		[]elf.Export{{Name: "fern_answer", Value: headers, Size: uint64(len(text))}}, "libfern.so")
	if e_type := u16(so, 16); e_type != 3 {
		t.Errorf("e_type = %d, want 3 (ET_DYN)", e_type)
	}
	if m := u16(so, 18); m != 183 {
		t.Errorf("e_machine = %d, want 183 (EM_AARCH64)", m)
	}
	if n := u16(so, 56); n != 3 {
		t.Errorf("e_phnum = %d, want 3", n)
	}
	// PH 2 (offset 64 + 2*56 = 176) is PT_DYNAMIC.
	if pt := u32(so, 176); pt != 2 {
		t.Errorf("PH2 p_type = %d, want 2 (PT_DYNAMIC)", pt)
	}
	if !bytes.Contains(so, []byte("fern_answer\x00")) {
		t.Errorf(".dynstr does not contain the export name")
	}
	if !bytes.Contains(so, []byte("libfern.so\x00")) {
		t.Errorf(".dynstr does not contain the soname")
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

// TestAssembledTestBranchRunsUnderQemu exercises tbz/tbnz end-to-end:
// bit 0 of x0 is set, so `tbz x0, #0` does NOT branch, falling through
// to set the result to 42.
func TestAssembledTestBranchRunsUnderQemu(t *testing.T) {
	src := "" +
		"\t.text\n" +
		"\tmov x0, #1\n" + // bit 0 set
		"\tmov x1, #99\n" +
		"\ttbz x0, #0, skip\n" + // bit 0 is 1 -> not taken
		"\tmov x1, #42\n" +
		"skip:\n" +
		"\tmov x0, x1\n" +
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

// TestStaticExecutableDataWXSyms checks the -g static symbol table: the
// image carries a parseable .symtab (function name → vaddr/size), and the
// loadable segments are byte-identical to the non-symtab image, so adding
// symbols never changes what runs.
func TestStaticExecutableDataWXSyms(t *testing.T) {
	text := []byte{0xb8, 0x2a, 0x00, 0x00, 0x00, 0xc3, 0x90, 0x90} // 8 bytes
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	base := uint64(elf.TextVAddrWX)
	syms := []elf.Sym{
		{Name: "main", Value: base, Size: 6},
		{Name: "helper", Value: base + 6, Size: 2},
	}

	for _, tc := range []struct {
		arch    string
		syms    []byte
		nosyms  []byte
		machine goelf.Machine
	}{
		{"x86-64", elf.StaticExecutableDataX86WXSyms(text, data, syms), elf.StaticExecutableDataX86WX(text, data), goelf.EM_X86_64},
		{"arm64", elf.StaticExecutableDataWXSyms(text, data, syms), elf.StaticExecutableDataWX(text, data), goelf.EM_AARCH64},
	} {
		f, err := goelf.NewFile(bytes.NewReader(tc.syms))
		if err != nil {
			t.Fatalf("%s: not a parseable ELF: %v", tc.arch, err)
		}
		if f.Type != goelf.ET_EXEC {
			t.Errorf("%s: type = %v, want ET_EXEC", tc.arch, f.Type)
		}
		if f.Machine != tc.machine {
			t.Errorf("%s: machine = %v, want %v", tc.arch, f.Machine, tc.machine)
		}
		got, err := f.Symbols()
		if err != nil {
			t.Fatalf("%s: Symbols(): %v", tc.arch, err)
		}
		want := map[string]struct{ v, s uint64 }{
			"main":   {base, 6},
			"helper": {base + 6, 2},
		}
		seen := map[string]bool{}
		for _, sym := range got {
			w, ok := want[sym.Name]
			if !ok {
				t.Errorf("%s: unexpected symbol %q", tc.arch, sym.Name)
				continue
			}
			seen[sym.Name] = true
			if sym.Value != w.v || sym.Size != w.s {
				t.Errorf("%s: %s = {value:0x%x size:%d}, want {0x%x %d}", tc.arch, sym.Name, sym.Value, sym.Size, w.v, w.s)
			}
			if goelf.ST_TYPE(sym.Info) != goelf.STT_FUNC {
				t.Errorf("%s: %s type = %v, want STT_FUNC", tc.arch, sym.Name, goelf.ST_TYPE(sym.Info))
			}
		}
		for n := range want {
			if !seen[n] {
				t.Errorf("%s: missing symbol %q", tc.arch, n)
			}
		}
		// The loadable image (everything past the 64-byte ELF header) must be
		// byte-identical to the non-symtab build: only the header's inert
		// section-table fields change, never a mapped byte.
		if len(tc.syms) < len(tc.nosyms) {
			t.Fatalf("%s: symtab image shorter than plain image", tc.arch)
		}
		if !bytes.Equal(tc.syms[64:len(tc.nosyms)], tc.nosyms[64:]) {
			t.Errorf("%s: loadable segments differ from the non-symtab image", tc.arch)
		}
	}
}

// TestStaticExecutableDataWXSymsDWARF checks the -g DWARF debug info (#5537
// slice 3): the same image carries a parseable .debug_info with a compilation
// unit spanning the text and one subprogram DIE per function (name + PC
// range), decodable through Go's debug/dwarf — which is what lets gdb/lldb
// break by function name and show frames in a backtrace.
func TestStaticExecutableDataWXSymsDWARF(t *testing.T) {
	text := []byte{0xb8, 0x2a, 0x00, 0x00, 0x00, 0xc3, 0x90, 0x90} // 8 bytes
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	base := uint64(elf.TextVAddrWX)
	syms := []elf.Sym{
		{Name: "main", Value: base, Size: 6},
		{Name: "helper", Value: base + 6, Size: 2},
	}
	for _, tc := range []struct {
		arch string
		bin  []byte
	}{
		{"x86-64", elf.StaticExecutableDataX86WXSyms(text, data, syms)},
		{"arm64", elf.StaticExecutableDataWXSyms(text, data, syms)},
	} {
		f, err := goelf.NewFile(bytes.NewReader(tc.bin))
		if err != nil {
			t.Fatalf("%s: not a parseable ELF: %v", tc.arch, err)
		}
		d, err := f.DWARF()
		if err != nil {
			t.Fatalf("%s: DWARF(): %v", tc.arch, err)
		}
		r := d.Reader()
		var sawCU bool
		subs := map[string][2]uint64{}
		for {
			e, err := r.Next()
			if err != nil {
				t.Fatalf("%s: DWARF reader: %v", tc.arch, err)
			}
			if e == nil {
				break
			}
			switch e.Tag {
			case dwarf.TagCompileUnit:
				sawCU = true
				lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
				hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
				if lo != base || hi != base+uint64(len(text)) {
					t.Errorf("%s: CU pc range = [%#x,%#x), want [%#x,%#x)", tc.arch, lo, hi, base, base+uint64(len(text)))
				}
			case dwarf.TagSubprogram:
				name, _ := e.Val(dwarf.AttrName).(string)
				lo, _ := e.Val(dwarf.AttrLowpc).(uint64)
				hi, _ := e.Val(dwarf.AttrHighpc).(uint64)
				subs[name] = [2]uint64{lo, hi}
			}
		}
		if !sawCU {
			t.Errorf("%s: no compile-unit DIE", tc.arch)
		}
		want := map[string][2]uint64{
			"main":   {base, base + 6},
			"helper": {base + 6, base + 8},
		}
		for n, w := range want {
			got, ok := subs[n]
			if !ok {
				t.Errorf("%s: missing subprogram DIE %q", tc.arch, n)
				continue
			}
			if got != w {
				t.Errorf("%s: subprogram %q pc = [%#x,%#x), want [%#x,%#x)", tc.arch, n, got[0], got[1], w[0], w[1])
			}
		}
	}
}

// TestStaticExecutableDataWXSymsRows checks the per-statement .debug_line path
// (#5537 slice 2, x86-64): a single line-number-program sequence over
// (address, line) rows decodes through debug/dwarf to exactly those rows, so
// gdb can step by source line inside a function (not just per function).
func TestStaticExecutableDataWXSymsRows(t *testing.T) {
	text := make([]byte, 32)
	for i := range text {
		text[i] = 0x90 // nops
	}
	text[len(text)-1] = 0xc3 // ret
	data := []byte{1, 2, 3, 4}
	base := uint64(elf.TextVAddrWX)
	syms := []elf.Sym{{Name: "main", Value: base, Size: uint64(len(text))}}
	rows := []elf.LineRow{
		{Addr: base, Line: 7},
		{Addr: base + 8, Line: 8},
		{Addr: base + 16, Line: 9},
	}
	bin := elf.StaticExecutableDataX86WXSymsRows(text, data, syms, rows, "prog.fern", "/tmp", base+uint64(len(text)), nil)

	f, err := goelf.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	d, err := f.DWARF()
	if err != nil {
		t.Fatalf("DWARF(): %v", err)
	}
	cu, err := d.Reader().Next()
	if err != nil || cu == nil {
		t.Fatalf("no CU: %v", err)
	}
	lr, err := d.LineReader(cu)
	if err != nil {
		t.Fatalf("LineReader: %v", err)
	}
	got := map[uint64]int{}
	var le dwarf.LineEntry
	for {
		if err := lr.Next(&le); err != nil {
			break
		}
		if !le.EndSequence {
			got[le.Address] = le.Line
		}
	}
	for _, r := range rows {
		if got[r.Addr] != r.Line {
			t.Errorf("addr %#x: line = %d, want %d (all rows: %v)", r.Addr, got[r.Addr], r.Line, got)
		}
	}
}

// TestDebugInfoLocalVars checks the -g DWARF variable DIEs (#5537 slice 3
// locals/params): a subprogram carries formal_parameter / variable child DIEs
// with a name, a DW_AT_type resolving to the right base type, and a
// frame-relative DW_AT_location — the information gdb/lldb use for `info args`
// / `info locals` / `print <var>`.
func TestDebugInfoLocalVars(t *testing.T) {
	text := make([]byte, 16)
	base := uint64(elf.TextVAddrWX)
	syms := []elf.Sym{{Name: "add", Value: base, Size: 16}}
	funcVars := map[string][]elf.LocalVar{
		"add": {
			{Name: "a", TypeKey: "i32", Offset: -8, IsParam: true},
			{Name: "b", TypeKey: "i32", Offset: -16, IsParam: true},
			{Name: "sum", TypeKey: "i32", Offset: -24, IsParam: false},
		},
	}
	bin := elf.StaticExecutableDataX86WXSymsRows(text, nil, syms, nil, "prog.fern", "/tmp", base+16, funcVars)

	f, err := goelf.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	d, err := f.DWARF()
	if err != nil {
		t.Fatalf("DWARF(): %v", err)
	}
	r := d.Reader()
	// Walk to the `add` subprogram, then read its children.
	var kids []*dwarf.Entry
	for {
		e, err := r.Next()
		if err != nil {
			t.Fatalf("reader: %v", err)
		}
		if e == nil {
			break
		}
		if e.Tag == dwarf.TagSubprogram {
			if name, _ := e.Val(dwarf.AttrName).(string); name != "add" {
				continue
			}
			for {
				c, err := r.Next()
				if err != nil {
					t.Fatalf("reader: %v", err)
				}
				if c == nil || c.Tag == 0 { // null entry ends the children
					break
				}
				kids = append(kids, c)
			}
			break
		}
	}
	if len(kids) != 3 {
		t.Fatalf("add: got %d child DIEs, want 3 (a, b, sum): %v", len(kids), kids)
	}
	wantParam := map[string]bool{"a": true, "b": true, "sum": false}
	for _, k := range kids {
		name, _ := k.Val(dwarf.AttrName).(string)
		isParam, ok := wantParam[name]
		if !ok {
			t.Errorf("unexpected variable DIE %q", name)
			continue
		}
		wantTag := dwarf.TagVariable
		if isParam {
			wantTag = dwarf.TagFormalParameter
		}
		if k.Tag != wantTag {
			t.Errorf("%s: tag = %v, want %v", name, k.Tag, wantTag)
		}
		// DW_AT_type resolves to an i32 base type.
		toff, ok := k.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			t.Errorf("%s: no DW_AT_type", name)
			continue
		}
		typ, err := d.Type(toff)
		if err != nil {
			t.Errorf("%s: resolve type: %v", name, err)
			continue
		}
		if typ.String() != "i32" || typ.Size() != 4 {
			t.Errorf("%s: type = %q size %d, want i32/4", name, typ.String(), typ.Size())
		}
		if _, ok := k.Val(dwarf.AttrLocation).([]byte); !ok {
			t.Errorf("%s: no DW_AT_location exprloc", name)
		}
	}
}

// TestFuncSyms checks the label→Sym conversion: assembler-local labels are
// dropped, symbols are sorted by address, and each size is the gap to the
// next symbol (the last runs to textEnd).
func TestFuncSyms(t *testing.T) {
	labels := map[string]uint64{
		"beta":   0x2000,
		"alpha":  0x1000,
		".Lloop": 0x1500, // assembler-local: must be dropped
	}
	got := elf.FuncSyms(labels, 0x2010)
	if len(got) != 2 {
		t.Fatalf("got %d syms, want 2 (%.v)", len(got), got)
	}
	if got[0].Name != "alpha" || got[0].Value != 0x1000 || got[0].Size != 0x1000 {
		t.Errorf("sym0 = %+v, want alpha@0x1000 size 0x1000", got[0])
	}
	if got[1].Name != "beta" || got[1].Value != 0x2000 || got[1].Size != 0x10 {
		t.Errorf("sym1 = %+v, want beta@0x2000 size 0x10", got[1])
	}
}
