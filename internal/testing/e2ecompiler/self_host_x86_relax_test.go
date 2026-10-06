package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

// TestSelfHostX86GasRelaxation pins the self-host assembler's branch
// relaxation byte for byte: the retired Go assembler's relaxation cases,
// whose bytes are GNU as's, plus two
// cascades where a later branch's growth pushes an earlier one out of rel8
// range, alignment pads among them (x86_relax_settle settles all of them on
// the first round's layout).
func TestSelfHostX86GasRelaxation(t *testing.T) {
	nops := func(n int) string { return strings.Repeat("nop\n", n) }
	hexNops := func(n int) string { return strings.Repeat("90", n) }
	cases := []struct{ src, want string }{
		{"L:\njmp L", "ebfe"},
		{"L:\njz L", "74fe"},
		{"L:\ncall L", "e8fbffffff"},
		{"jmp L\nret\nL:\nret", "eb01c3c3"},
		{"jne L\nret\nL:\nret", "7501c3c3"},
		{"jmp L\nL:\nret", "eb00c3"},
		{"jmp M\nL: jmp M\nM: ret", "eb02eb00c3"},
		{"jmp L\n" + nops(127) + "L: ret", "eb7f" + hexNops(127) + "c3"},
		{"jmp L\n" + nops(128) + "L: ret", "e980000000" + hexNops(128) + "c3"},
		{"L:\n" + nops(126) + "jmp L", hexNops(126) + "eb80"},
		{"L:\n" + nops(127) + "jmp L", hexNops(127) + "e97cffffff"},
		{"L:\n" + nops(126) + "jz L", hexNops(126) + "7480"},
		{"L:\n" + nops(127) + "jz L", hexNops(127) + "0f847bffffff"},
		// Chain reaction: the first jmp is in range only once the second is short.
		{"jmp L1\n" + nops(124) + "jmp L2\nL2:\nL1: ret", "eb7e" + hexNops(124) + "eb00c3"},
		// Cascades: the second branch cannot reach L2, and its growth puts L1
		// out of the first one's reach, so the first goes long a round later.
		{"jmp L1\n" + nops(125) + "jmp L2\nL1:\n" + nops(200) + "L2: ret", "e982000000" + hexNops(125) + "e9c8000000" + hexNops(200) + "c3"},
		{"jz L1\n" + nops(124) + "jz L2\nL1:\n" + nops(200) + "L2: ret", "0f8482000000" + hexNops(124) + "0f84c8000000" + hexNops(200) + "c3"},
		// Alignment in .text: two self-consistent layouts, of which grow-only
		// relaxation must land on the short one; and a growth the pad before
		// the target absorbs, so the later branch stays short (#11001).
		{"A:\nnop\njmp A\njs L\n" + nops(120) + ".p2align 4\nL: ret", "90ebfd787b" + hexNops(120) + "0f1f00" + "c3"},
		{"A:\n" + nops(140) + "jle A\njno L\n" + nops(120) + ".p2align 4\nL: ret", hexNops(140) + "0f8e6effffff" + "717c" + hexNops(120) + "0f1f4000" + "c3"},
		// A label written right after a pad that is empty in the first round
		// stays on its far edge when the first jmp's growth widens the pad to
		// five bytes, which puts it out of the second jmp's reach.
		{"jmp L\n" + nops(8) + "jmp M\n" + nops(124) + ".p2align 3\nM:\n" + nops(200) + "L: ret", "e953010000" + hexNops(8) + "e97e000000" + hexNops(124) + "6690" + hexNops(200) + "c3"},
	}
	var body strings.Builder
	for i, c := range cases {
		fmt.Fprintf(&body, "    if (relax_hex(%q) != %q) { return %d; }\n", c.src+"\n", c.want, i+1)
	}
	runX86GasWasmSelfTest(t, "x86_gas_relax", `
function relax_hex(src: string): string {
    let a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return "unknown"; }
    let digits: string = "0123456789abcdef";
    let out: string = "";
    for c in a.text {
        let b: i32 = c as i32;
        out = out + slice_unchecked(digits, b / 16, b / 16 + 1) + slice_unchecked(digits, b % 16, b % 16 + 1);
    }
    return out;
}
function main(): i32 {
`+body.String()+`    return 0;
}
`)
}

// TestSelfHostX86RelaxMovesRows pins where relaxation leaves the .loc rows
// and CFI offsets written around a pad the first round laid out empty. Both
// jmps go long, as in the last relaxation case, so the pad at offset 136
// widens to two bytes. What was written before the .p2align stays in front
// of it, at 142; what was written after it, at the same first-round offset,
// moves to its far edge at 144 with M.
func TestSelfHostX86RelaxMovesRows(t *testing.T) {
	nops := strings.Repeat("nop\n", 8)
	src := "jmp L\n" + nops + "jmp M\n" + strings.Repeat("nop\n", 124) +
		".cfi_startproc\n.loc 1 1 1\n.p2align 3\n.cfi_def_cfa_offset 16\n.loc 1 2 1\nM:\n" +
		strings.Repeat("nop\n", 200) + "L: ret\n.cfi_endproc\n"
	runX86GasWasmSelfTest(t, "x86_relax_rows", fmt.Sprintf(`
function main(): i32 {
    let a: X86Asm = x86_gas_assemble(%q);
    if (a.unknown.len() > 0) { return 1; }
    if (a.text.len() != 345) { return 2; }
    if (a.loc_offs.len() != 2 || a.loc_offs[0] != 142 || a.loc_offs[1] != 144) { return 3; }
    if (a.cfi.fde_start.len() != 1 || a.cfi.fde_start[0] != 142) { return 4; }
    if (a.cfi.rule_off.len() != 1 || a.cfi.rule_off[0] != 144) { return 5; }
    if (a.cfi.fde_end.len() != 1 || a.cfi.fde_end[0] != 345) { return 6; }
    if (x86_label_off(a, "M") != 144) { return 7; }
    return 0;
}
`, src))
}
