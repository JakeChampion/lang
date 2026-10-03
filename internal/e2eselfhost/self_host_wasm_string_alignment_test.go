package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

const wasmStringAlignmentProgram = `import "./wasm_ir";
function main(): i32 {
    let values: string[] = ["", "a", "ab", "abc", "abcd", "é", "界", "𐐀"];
    let base: i32 = wasm_ir.str_region_base();
    let i: i32 = 0;
    while (i <= values.len()) {
        let offset = wasm_ir.str_offset(values, i);
        if (offset % 4 != 0) { return 1; }
        if (i > 0 && offset < wasm_ir.str_offset(values, i - 1) + 4 + values[i - 1].len()) { return 2; }
        let left: string[] = [];
        let right: string[] = [];
        let j: i32 = 0;
        while (j < values.len()) {
            if (j < i) { left = left.append(values[j]); } else { right = right.append(values[j]); }
            j = j + 1;
        }
        let right_base = wasm_ir.str_offset(left, left.len());
        j = 0;
        while (j <= right.len()) {
            if (wasm_ir.str_offset(values, i + j) != right_base + wasm_ir.str_offset(right, j) - base) { return 3; }
            j = j + 1;
        }
        i = i + 1;
    }
    return 0;
}
`

// Literal pointers and unit bases must preserve the same alignment when a
// module is split at any literal, including empty and multibyte strings.
func TestSelfHostWasmStringAlignment(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir.fern")
	if err := os.WriteFile(filepath.Join(dir, "alignment.fern"), []byte(wasmStringAlignmentProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "alignment.fern", "alignment")
	if out, err := runX86_64Bin(runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("string alignment: %v\n%s", err, out)
	}
}
