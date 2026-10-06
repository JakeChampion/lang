package e2ecompiler

import "testing"

// A `.bss` data directive with a value list reserves one zero-init slot per
// value. The emitters write their multi-word globals as one labelled line
// (`__fern_task_state: .quad 0, 0, 0, 0`), and an assembler reserving one
// slot for the whole list puts every later .bss symbol on top of the list's
// tail: the task runtime read the heap allocator's bump pointer as its record
// table and every self-host-built arm64 server faulted on its first request
// (#11377).
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
