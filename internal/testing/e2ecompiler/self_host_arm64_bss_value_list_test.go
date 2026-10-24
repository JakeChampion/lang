package e2ecompiler

import "testing"

const arm64BssValueListProgram = `
function main(): i32 {
    let p = arm64_gas_program(".bss\nfour: .quad 0, 0, 0, 0\npair: .word 1, 2\nhalf: .4byte 3, 4\nlast: .skip 1\n");
    if (p.unknown.len() != 0 || p.data.len() != 0) { return 1; }
    if (arm64_gas_bss_off(p, "four") != 0) { return 2; }
    if (arm64_gas_bss_off(p, "pair") != 32) { return 3; }
    if (arm64_gas_bss_off(p, "half") != 40) { return 4; }
    if (arm64_gas_bss_off(p, "last") != 48) { return 5; }
    if (p.bss_size != 49) { return 6; }
    let one = arm64_gas_program(".bss\nword: .quad 0\nnext: .word 0\nend: .skip 1\n");
    if (arm64_gas_bss_off(one, "next") != 8 || arm64_gas_bss_off(one, "end") != 12 || one.bss_size != 13) { return 7; }
    return 0;
}
`

func TestSelfHostArm64BssValueList(t *testing.T) {
	cli := buildSelfHostCLI(t)
	source := arm64NativeSrc(t) + arm64BssValueListProgram
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, source, target); code != 0 {
				t.Fatalf("exit = %d\n%s", code, stderr)
			}
		})
	}
}
