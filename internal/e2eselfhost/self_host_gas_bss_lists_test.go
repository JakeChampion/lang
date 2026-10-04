package e2eselfhost

import "testing"

// A zero-init `.quad` or `.word` list in .bss reserves a slot for each value,
// as gas does: the task state block is `.quad 0, 0, 0, 0`, and reserving
// one word for it laid the next .bss symbol over its last three (#11380).
const arm64GasBssListsProgram = `
function main(): i32 {
    let p: Arm64GasProg = arm64_gas_program(".section .bss\n.p2align 3\nstate: .quad 0, 0, 0, 0\npair: .word 0, 0\nnext: .skip 1\n");
    if (p.unknown.len() != 0 || p.data.len() != 0) { return 1; }
    if (arm64_gas_bss_off(p, "pair") != 32) { return 2; }
    if (arm64_gas_bss_off(p, "next") != 40 || p.bss_size != 41) { return 3; }
    return 0;
}
`

const x86GasBssListsProgram = `
function main(): i32 {
    let a: X86Asm = x86_gas_assemble(".section .bss\n.align 8\nstate: .quad 0, 0, 0, 0\nnext: .quad 0\n");
    if (a.unknown.len() != 0) { return 1; }
    let at: i32 = x86_label_idx(a, "next");
    if (at < 0 || a.lab_secs[at] != 2 || a.lab_offs[at] != 32) { return 2; }
    if (a.bss_size != 40) { return 3; }
    return 0;
}
`

func TestSelfHostGasBssListsReserveEveryValue(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range []struct{ name, src string }{
		{"arm64", arm64NativeSrc(t) + arm64GasBssListsProgram},
		{"x86", string(mustRead(t, "../../examples/self_host/x86_native.fern")) + "\n" + x86GasBssListsProgram},
	} {
		t.Run(c.name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, c.src, "x86-64-linux", "FERN_STRICT_IR=1"); code != 0 {
				t.Fatalf("failed at check %d\n%s", code, stderr)
			}
		})
	}
}
