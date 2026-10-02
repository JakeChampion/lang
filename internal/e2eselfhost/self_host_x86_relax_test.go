package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// TestSelfHostX86GasRelaxation pins the self-host assembler's branch
// relaxation byte for byte: the native assembler's relaxation cases
// (internal/native/x86_64/relax_test.go), whose bytes are GNU as's, plus two
// cascades where a later branch's growth pushes an earlier one out of rel8
// range. Without alignment in .text the fixpoint is settled on the first
// round's offsets (x86_relax_settle); the .p2align case takes the
// round-by-round path.
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
		// relaxation must land on the short one.
		{"A:\nnop\njmp A\njs L\n" + nops(120) + ".p2align 4\nL: ret", "90ebfd787b" + hexNops(120) + "0f1f00" + "c3"},
	}
	var body strings.Builder
	for i, c := range cases {
		fmt.Fprintf(&body, "    if (relax_hex(%q) != %q) { return %d; }\n", c.src+"\n", c.want, i+1)
	}
	runX86GasWasmSelfTest(t, "x86_gas_relax", `
function relax_hex(src: string): string {
    var a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return "unknown"; }
    var digits: string = "0123456789abcdef";
    var out: string = "";
    for b in a.code {
        out = out + slice_unchecked(digits, b / 16, b / 16 + 1) + slice_unchecked(digits, b % 16, b % 16 + 1);
    }
    return out;
}
function main(): i32 {
`+body.String()+`    return 0;
}
`)
}
