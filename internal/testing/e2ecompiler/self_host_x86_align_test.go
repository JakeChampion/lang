package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// The driver assembles a text and a read-only section with `.align 32` and
// `.align 64`, links them at the text address of each x86-64 image, and reads
// the address of each label. Its exit status is the first check that failed.
const x86AlignDriver = `import "./x86_native"; import "./elf";
function aligned(a: x86_native.X86Asm, tv: i32): i32 {
    let l = x86_native.x86_link(a, tv, tv + 8192);
    if (l.unknown.len() != 0) { return 2; }
    if (x86_native.x86_symbol_addr(l, "c32", tv) % 32 != 0) { return 3; }
    if (x86_native.x86_symbol_addr(l, "c64", tv) % 64 != 0) { return 4; }
    if (x86_native.x86_symbol_addr(l, "t32", tv) % 32 != 0) { return 5; }
    return 0;
}
function main(): i32 {
    let asm = "    .text\n_start:\n    ret\n    .align 32\nt32:\n    ret\n    .section .rodata\n    .byte 1\n    .align 32\nc32:\n    .quad 1\n    .byte 2\n    .align 64\nc64:\n    .quad 2\n    .text\n";
    let a = x86_native.x86_gas_assemble_words_unlinked(asm, []);
    if (a.unknown.len() != 0) { return 1; }
    for tv in [elf.elf_x86_wx_text_vaddr(), elf.elf_x86_pie_text_vaddr(), elf.elf_text_off_so(false), elf.elf_text_off_so(true), elf.elf_text_off_pvh()] {
        let bad = aligned(a, tv);
        if (bad != 0) { return bad; }
    }
    let data = x86_native.x86_gas_assemble_words_unlinked("    .data\n    .align 32\nd32:\n    .quad 1\n    .text\n", []);
    if (data.unknown.len() == 0) { return 6; }
    let wide = x86_native.x86_gas_assemble_words_unlinked("    .section .rodata\n    .align 128\nw:\n    .quad 1\n    .text\n", []);
    if (wide.unknown.len() == 0) { return 7; }
    if (x86_native.x86_link(a, 16, 8192).unknown.len() == 0) { return 8; }
    return 0;
}
`

// `.align 32` and `.align 64` hold for the address of a label in the text and
// in the read-only data of each x86-64 image (#15755). A data section, whose
// base is a multiple of 16 only, still refuses them.
func TestSelfHostX86AlignHoldsForAddresses(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "align.fern"), []byte(x86AlignDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "align.fern", "align")
	cmd := runX86_64Bin(runner, driver)
	out, _ := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("check %d failed\n%s", code, out)
	}
}
