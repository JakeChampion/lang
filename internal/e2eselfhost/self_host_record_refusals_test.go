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
// well-formed twin of each must assemble clean. A named record carrying an
// adrp or its :lo12: add must queue the page fixup the text arm queues.
const recordRefusalsDriver = `import "./x86_native"; import "./arm64_native"; import "./util";
function first_diff(a: i32[], b: i32[]): i32 {
    let n: i32 = a.len();
    if (b.len() < n) { n = b.len(); }
    let i: i32 = 0;
    while (i < n) {
        if (a[i] != b[i]) { return i; }
        i = i + 1;
    }
    if (a.len() != b.len()) { return n; }
    return 0 - 1;
}
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
    // arm64: a named record carrying adrp or the :lo12: add is the symbol's
    // page fixup, queued as the text arms queue theirs.
    let addr: usize = buf_new(16);
    buf_push_u64(addr, arm64_native.arm64_rec_adrp("x3") as u64);
    buf_push_u64(addr, arm64_native.arm64_rec_add_lo12("x3", "x3") as u64);
    let tail: string = ".data\nsym:\n    .quad 7\n";
    let pt = arm64_native.arm64_gas_program("    adrp x3, sym\n    add x3, x3, :lo12:sym\n" + tail);
    let pr = arm64_native.arm64_gas_program_words("\x02sym\n\x02sym\n" + tail, buf_take_bytes(addr));
    print("arm64_named_unknown " + util.i32_to_string(pr.unknown.len()));
    print("arm64_named_code_diff " + util.i32_to_string(first_diff(pt.asm.code, pr.asm.code)));
    print("arm64_named_fixups " + util.i32_to_string(pr.pf_sites.len()));
    let fi: i32 = 0;
    while (fi < pr.pf_sites.len() && fi < pt.pf_sites.len()) {
        if (pr.pf_sites[fi] != pt.pf_sites[fi] || pr.pf_syms[fi] != pt.pf_syms[fi] || pr.pf_kinds[fi] != pt.pf_kinds[fi]) {
            print("arm64_named_fixup_mismatch " + util.i32_to_string(fi));
        }
        fi = fi + 1;
    }
    print("arm64_named_fixups_text " + util.i32_to_string(pt.pf_sites.len()));
    // arm64: a pair mode the text fallback cannot spell, and a w register
    // beside sp, are refused rather than encoded.
    print("arm64_pair_mode2 " + util.i64_to_string(arm64_native.arm64_rec_pair(false, 2, "x19", "x20", "sp", 16)));
    print("arm64_pair_mode3 " + util.i64_to_string(arm64_native.arm64_rec_pair(false, 3, "x19", "x20", "sp", 0 - 16)));
    print("arm64_mov_w_sp " + util.i64_to_string(arm64_native.arm64_rec_mov("w0", "sp")));
    print("arm64_mov_sp_w " + util.i64_to_string(arm64_native.arm64_rec_mov("sp", "w0")));
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
	if bytes.Contains(out, []byte("arm64_named_fixup_mismatch")) {
		t.Errorf("a named record's fixup differs from the text arm's:\n%s", out)
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
		"\narm64_named_unknown 0\n",
		"\narm64_named_code_diff -1\n",
		"\narm64_named_fixups 2\n",
		"\narm64_named_fixups_text 2\n",
		"\narm64_pair_mode2 -1\n",
		"\narm64_pair_mode3 2847888371\n", // stp x19, x20, [sp, #-16]! is 0xa9bf53f3
		"\narm64_mov_w_sp -1\n",
		"\narm64_mov_sp_w -1\n",
	} {
		if !bytes.Contains(append([]byte("\n"), out...), []byte(want)) {
			t.Errorf("missing %q in driver output:\n%s", want[1:len(want)-1], out)
		}
	}
}
