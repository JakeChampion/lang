package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const darwinSyscallRemapSource = `import "./asm_arm64_ir";
import "./util";

function has(s: string, sub: string): boolean {
    return util.index_of_sub(s, sub) >= 0;
}

function main(): i32 {
    let value: string = asm_arm64_ir.darwinize("    mov x8, #63\n    add x0, x8, #1\n    ret\n");
    if (!has(value, "mov x8, #63") || has(value, "x16")) { return 1; }
    let call: string = asm_arm64_ir.darwinize("    mov x8, #63\n    mov x0, x1\n    str x8, [sp, #8]\n    svc #0\n");
    if (!has(call, "mov x16, #3") || !has(call, "svc #0x80")) { return 2; }
    let crossed: string = asm_arm64_ir.darwinize("    mov x8, #64\n    b L1\nL1:\n    mov x8, #57\n    svc #0\n");
    if (!has(crossed, "mov x8, #64") || !has(crossed, "mov x16, #6")) { return 3; }
    let popped: string = asm_arm64_ir.darwinize("    ldr x8, [sp], #16\n    add x0, x8, #1\n    ret\n");
    if (!has(popped, "ldr x8, [sp], #16")) { return 4; }
    return 0;
}
`

func TestSelfHostDarwinRemapsOnlySyscallNumbers(t *testing.T) {
	runner := x86_64Runner(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "remap.fern"), []byte(darwinSyscallRemapSource), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBinFor(t, dir, "remap.fern", "remap", "x86-64-linux")
	if out, err := runX86_64Bin(runner, driver).CombinedOutput(); err != nil {
		t.Fatalf("darwinize remap: %v\n%s", err, out)
	}
}
