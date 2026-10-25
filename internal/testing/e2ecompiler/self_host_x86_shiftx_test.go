package e2ecompiler

import "testing"

// The bytes are GNU as 2.42's for the same lines.
func TestSelfHostX86GasShiftx(t *testing.T) {
	runX86GasWasmSelfTest(t, "x86_gas_shiftx", x86GasShiftxMain)
}

const x86GasShiftxMain = `
function main(): i32 {
    let src: string = "    .text\n_start:\n    shlxq %rcx, %rax, %rbx\n    shrxq %r9, %r10, %r11\n    sarxq %rdx, %r8, %rsi\n    shlxl %ebx, %r12d, %eax\n    shrxl %r15d, %ecx, %r14d\n    sarxl %eax, %eax, %eax\n    shlxq %rdi, -80(%rbp), %r13\n    shrxq %r11, 8(%r12,%r13,4), %rdx\n";
    let a: X86Asm = x86_gas_assemble(src);
    if (a.unknown.len() > 0) { return 90; }
    let exp: i32[] = [196, 226, 241, 247, 216, 196, 66, 179, 247, 218, 196, 194, 234, 247, 240, 196, 194, 97, 247, 196, 196, 98, 3, 247, 241, 196, 226, 122, 247, 192, 196, 98, 193, 247, 109, 176, 196, 130, 163, 247, 84, 172, 8];
    if (a.text.len() != exp.len()) { return 91; }
    let i: i32 = 0;
    while (i < exp.len()) {
        if (a.text[i] as i32 != exp[i]) { return i + 1; }
        i = i + 1;
    }
    let bad: X86Asm = x86_gas_assemble("    .text\n_start:\n    shlxq %rcx, %rax\n    shlxq %ecx, %rax, %rbx\n");
    if (bad.unknown.len() != 2) { return 92; }
    return 0;
}
`
