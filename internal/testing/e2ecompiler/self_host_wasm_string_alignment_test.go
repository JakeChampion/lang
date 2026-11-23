package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
        if (i > 0 && offset < wasm_ir.str_offset(values, i - 1) + 8 + values[i - 1].len()) { return 2; }
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

// A literal is a box [data, len] in the data segment: the absolute address of
// its bytes, its length, and then the bytes (#15740).
func TestSelfHostWasmStringLiteralIsABox(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    print("box-literal");
    return 0;
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	wat, err := os.ReadFile(cli.emit(t, src, "wasm32-wasi"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\(data \(i32\.const (\d+)\) "((?:\\[0-9a-f]{2}){8})box-literal"\)`).FindSubmatch(wat)
	if m == nil {
		t.Fatalf("no data segment holds the box of \"box-literal\":\n%s", wat)
	}
	off, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	word := func(at int) int {
		v := 0
		for i := 3; i >= 0; i-- {
			b, err := strconv.ParseUint(string(m[2][at+i*3+1:at+i*3+3]), 16, 8)
			if err != nil {
				t.Fatal(err)
			}
			v = v<<8 | int(b)
		}
		return v
	}
	if data := word(0); data != off+8 {
		t.Errorf("data word is %d, want %d (the block + 8)", data, off+8)
	}
	if n := word(12); n != len("box-literal") {
		t.Errorf("length word is %d, want %d", n, len("box-literal"))
	}
}
