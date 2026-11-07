package e2ecompiler

import "testing"

const arm64DataListsProgram = `
function main(): i32 {
    let bss = arm64_gas_program(".bss\nls: .long 0, 0, 0\nds: .double 0, 0\nbs: .byte 0, 0\nend: .skip 1\n");
    if (bss.unknown.len() != 0 || bss.data.len() != 0 || bss.bss_size != 31) { return 1; }
    if (arm64_gas_bss_off(bss, "ds") != 12 || arm64_gas_bss_off(bss, "bs") != 28 || arm64_gas_bss_off(bss, "end") != 30) { return 2; }
    let data = arm64_gas_program(".data\nqs: .quad 1, -2\nws: .4byte 3, 4\nw1: .word 5\nls: .long 6, 7\nnext: .byte 9\n");
    if (data.unknown.len() != 0 || arm64_gas_dlabel_off(data, "next") != 36) { return 5; }
    if (data.data[0] != 1 || data.data[8] != 254 || data.data[15] != 255) { return 6; }
    if (data.data[16] != 3 || data.data[20] != 4 || data.data[24] != 5 || data.data[28] != 6 || data.data[32] != 7 || data.data[36] != 9) { return 7; }
    // Each symbol in a .quad list is its own relocation.
    let syms = arm64_gas_program(".data\nt: .quad t, 0, t\n");
    if (syms.data.len() != 24 || syms.dref_offs.len() != 2 || syms.dref_offs[0] != 0 || syms.dref_offs[1] != 16) { return 8; }
    // A sym + N addend stays in the word, and the link adds the address.
    let plus = arm64_gas_program(".data\nt: .quad t, t + 3, t+16\n");
    if (plus.unknown.len() != 0 || plus.dref_syms.len() != 3 || plus.dref_syms[1] != "t" || plus.dref_syms[2] != "t") { return 9; }
    if (plus.data[0] != 0 || plus.data[8] != 3 || plus.data[16] != 16) { return 10; }
    plus = arm64_gas_link(plus, 4096i64, 131072i64);
    if (plus.unknown.len() != 0 || plus.data[8] as i64 != plus.data[0] as i64 + 3i64 || plus.data[16] as i64 != plus.data[0] as i64 + 16i64) { return 11; }
    for bad in [".data\n.quad t + x\n", ".data\n.quad t +\n"] {
      if (arm64_gas_program(bad).unknown.len() != 1) { return 12; }
    }
    return 0;
}
`

func TestSelfHostArm64DataLists(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := writeAssemblerProgram(t, writeSelfHostAsmProject(t), "arm64_native.fern", arm64DataListsProgram)
	if stderr, code := cli.exitOfFile(t, source, "x86-64-linux", nil, "FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_STRICT_IR=1"); code != 0 {
		t.Fatalf("exit = %d\n%s", code, stderr)
	}
}
