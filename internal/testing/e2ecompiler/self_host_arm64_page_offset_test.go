package e2ecompiler

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The data words sit at offsets 8 and 16, and the byte puts the last quad
// at offset 25, which no scaled load can reach.
const arm64PageOffsetDriver = `import "./arm64_native"; import "./util";
function word(b: u8[], at: i32): i64 {
    return b[at] as i64 | (b[at + 1] as i64 << 8) | (b[at + 2] as i64 << 16) | (b[at + 3] as i64 << 24);
}
function report(name: string, src: string): i32 {
    let p = arm64_native.arm64_gas_link(arm64_native.arm64_gas_program(src), 4096i64, 131072i64);
    let line: string = name;
    for u in p.unknown { line = line + " [" + u + "]"; }
    let at: i32 = 0;
    while (p.unknown.len() == 0 && at + 4 <= p.asm.text.len()) {
        line = line + " " + util.i64_to_string(word(p.asm.text, at));
        at = at + 4;
    }
    print(line);
    return 0;
}
function main(): i32 {
    let data: string = ".data\nfirst:\n    .quad 1\nquad:\n    .quad 7\nwide:\n    .long 3\n    .long 0\nbyte:\n    .byte 1\nodd:\n    .quad 9\n";
    report("ldr_x", ".text\n    ldr x16, [x16, :lo12:quad]\n" + data);
    report("ldr_w", ".text\n    ldr w1, [x1, :lo12:wide]\n" + data);
    report("ldr_pageoff", ".text\n    ldr x16, [x16, quad@PAGEOFF]\n" + data);
    report("ldr_odd", ".text\n    ldr x0, [x0, :lo12:odd]\n" + data);
    report("str_lo12", ".text\n    str x0, [x1, :lo12:quad]\n" + data);
    return 0;
}
`

// TestSelfHostArm64PageOffsetLoads pins the low-12 load of a symbol in the
// in-process assembler. The offset is the symbol's page offset scaled by the
// access size, a misaligned target is refused, and a page offset in an
// instruction with no such fixup is refused rather than read as a number.
func TestSelfHostArm64PageOffsetLoads(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "pageoff.fern"), []byte(arm64PageOffsetDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "pageoff.fern", "pageoff")
	cmd := runX86_64Bin(runner, driver)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, stderr.String())
	}
	for _, want := range []string{
		// ldr x16, [x16, #8]: 0xf9400610.
		"ldr_x 4181722640\n",
		// ldr w1, [x1, #16]: 0xb9401021.
		"ldr_w 3107983393\n",
		"ldr_pageoff 4181722640\n",
		"ldr_odd [ldr:align:odd]\n",
		"str_lo12 [page offset in str]\n",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("driver output lacks %q:\n%s", want, out)
		}
	}
}
