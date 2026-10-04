package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// recordRefusalsDriver hands each assembler a record stream by hand. A
// branch record to a label id nothing defines, and a marker byte the arm64
// assembler has no record for, must be refused rather than assembled; the
// well-formed twin of each must assemble clean.
const recordRefusalsDriver = `import "./x86_native"; import "./arm64_native"; import "./util";
function pack(id: i32, payload: i64): u8[] {
    let recs: usize = buf_new(16);
    buf_push_u64(recs, (id as i64 << 32 | payload & 4294967295i64) as u64);
    return buf_take_bytes(recs);
}
function main(): i32 {
    // x86: jmp (kind 16) to label id 7 with no definition, then with one.
    let bad = x86_native.x86_gas_assemble_words(".text\n\x05\n", pack(7, 16));
    print("x86_undefined_unknown " + util.i32_to_string(bad.unknown.len()));
    for u in bad.unknown { print("  " + u); }
    let defs: usize = buf_new(16);
    buf_push_u64(defs, (7i64 << 32 | 16) as u64);
    buf_push_u64(defs, 7 as u64);
    let good = x86_native.x86_gas_assemble_words(".text\n\x05\n\x04\n", buf_take_bytes(defs));
    print("x86_defined_unknown " + util.i32_to_string(good.unknown.len()));
    print("x86_defined_code " + util.i32_to_string(good.code.len()));
    // arm64: a \x03 line is text, refused as text, and leaves its word unread.
    let nop: usize = buf_new(16);
    buf_push_u64(nop, 3573751839 as u64);
    let a3 = arm64_native.arm64_gas_program_words("\x03nop\n", buf_take_bytes(nop));
    print("arm64_mark3_unknown " + util.i32_to_string(a3.unknown.len()));
    for u in a3.unknown { print("  " + u); }
    print("arm64_mark3_code " + util.i32_to_string(a3.asm.code.len()));
    let nop1: usize = buf_new(16);
    buf_push_u64(nop1, 3573751839 as u64);
    let a1 = arm64_native.arm64_gas_program_words("\x01\n", buf_take_bytes(nop1));
    print("arm64_mark1_unknown " + util.i32_to_string(a1.unknown.len()));
    print("arm64_mark1_code " + util.i32_to_string(a1.asm.code.len()));
    return 0;
}
`

func TestSelfHostRecordRefusals(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	if err := os.WriteFile(filepath.Join(dir, "refusals.fern"), []byte(recordRefusalsDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "refusals.fern", "refusals")
	cmd := runX86_64Bin(runner, driver)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("driver: %v\n%s", err, stderr.String())
	}
	for _, want := range []string{
		"\nx86_undefined_unknown 1\n",
		"\n  branch to a label id nothing defines\n",
		"\nx86_defined_unknown 0\n",
		"\nx86_defined_code 2\n",    // jmp to the next byte is EB 00
		"\narm64_mark3_unknown 2\n", // the text line, and the word it left unread
		"\narm64_mark3_code 0\n",
		"\narm64_mark1_unknown 0\n",
		"\narm64_mark1_code 4\n",
	} {
		if !bytes.Contains(append([]byte("\n"), out...), []byte(want)) {
			t.Errorf("missing %q in driver output:\n%s", want[1:len(want)-1], out)
		}
	}
}
