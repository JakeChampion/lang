package e2eselfhost

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// recordRefusalsDriver hands each assembler a record stream by hand. A
// branch record to a label id nothing defines, and a marker byte the arm64
// assembler has no record for, must be refused rather than assembled; the
// well-formed twin of each must assemble clean. A named record carrying an
// adrp or its :lo12: add must queue the page fixup the text arm queues. Each
// x86 record encoder of the frame, the stack and the memory forms must
// assemble to the bytes its text assembles to, a named record must be the
// call or rip-relative address its text assembles to, and a frame's .cfi_*
// records must render the unwind image their directives render, on both
// targets; the arm64 sxtw record must be its text's word and refuse the
// operands sxtw does not take.
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
function words_of(b: i32[]): u8[] {
    let w: usize = buf_new(32);
    buf_push_byte(w, b.len());
    for x in b { buf_push_byte(w, x); }
    return buf_take_bytes(w);
}
// rec_vs_text assembles one x86 instruction as a bytes record and as text and
// prints the first byte they differ at, -1 when they agree, then the count of
// lines either assembly refused.
function rec_vs_text(name: string, b: i32[], text: string): i32 {
    let r = x86_native.x86_gas_assemble_words(".text\n\x01\n", words_of(b));
    let t = x86_native.x86_gas_assemble_words(".text\n    " + text + "\n", buf_take_bytes(buf_new(1)));
    print(name + " " + util.i32_to_string(first_diff(r.code, t.code)) + " " + util.i32_to_string(r.unknown.len() + t.unknown.len()));
    return 0;
}
// cfi_word packs a .cfi_* record as asmcore's cfi does: the kind and the DWARF
// register in the low word, the offset in the high one.
function cfi_word(w: usize, kind: i32, r: i32, n: i32): void {
    buf_push_u64(w, ((n as i64 & 4294967295i64) << 32 | r as i64 << 8 | kind as i64) as u64);
}
function bytes_into(w: usize, b: i32[]): void {
    for x in words_of(b) { buf_push_byte(w, x as i32); }
}
function main(): i32 {
    // Both targets: a frame's .cfi_* directives as records render the unwind
    // image their text renders, around the same instructions.
    let cx: usize = buf_new(128);
    cfi_word(cx, 1, 0, 0);
    bytes_into(cx, x86_native.x86_rec_push_r(5));
    cfi_word(cx, 3, 0, 16);
    cfi_word(cx, 6, 6, 0 - 16);
    bytes_into(cx, x86_native.x86_rec_mov_rr(64, 5, 4));
    cfi_word(cx, 4, 6, 0);
    cfi_word(cx, 7, 0, 0);
    bytes_into(cx, x86_native.x86_rec_pop_r(5));
    cfi_word(cx, 5, 7, 8);
    bytes_into(cx, x86_native.x86_rec_ret());
    cfi_word(cx, 8, 0, 0);
    cfi_word(cx, 2, 0, 0);
    let xr = x86_native.x86_gas_assemble_words(".text\nf:\n\x06\n\x01\n\x06\n\x06\n\x01\n\x06\n\x06\n\x01\n\x06\n\x01\n\x06\n\x06\n", buf_take_bytes(cx));
    let xt = x86_native.x86_gas_assemble_words(".text\nf:\n    .cfi_startproc\n    pushq %rbp\n    .cfi_def_cfa_offset 16\n    .cfi_offset %rbp, -16\n    movq %rsp, %rbp\n    .cfi_def_cfa_register %rbp\n    .cfi_remember_state\n    popq %rbp\n    .cfi_def_cfa %rsp, 8\n    ret\n    .cfi_restore_state\n    .cfi_endproc\n", buf_take_bytes(buf_new(1)));
    let xeh: i32[] = x86_native.x86_eh_frame(xr, 0 as i64, 0 as i64);
    print("x86_cfi " + util.i32_to_string(first_diff(xeh, x86_native.x86_eh_frame(xt, 0 as i64, 0 as i64))) + " " + util.i32_to_string(first_diff(xr.code, xt.code)) + " " + util.i32_to_string(xr.unknown.len() + xt.unknown.len()));
    print("x86_cfi_eh " + util.i32_to_string(xeh.len()));
    let cbad: usize = buf_new(16);
    cfi_word(cbad, 9, 0, 0);
    let xb = x86_native.x86_gas_assemble_words(".text\n\x06\n", buf_take_bytes(cbad));
    print("x86_cfi_bad " + util.i32_to_string(xb.unknown.len()));
    let ca: usize = buf_new(128);
    cfi_word(ca, 1, 0, 0);
    buf_push_u64(ca, arm64_native.arm64_rec_pair(false, 3, "x29", "x30", "sp", 0 - 16) as u64);
    cfi_word(ca, 3, 0, 16);
    cfi_word(ca, 6, 29, 0 - 16);
    cfi_word(ca, 6, 30, 0 - 8);
    buf_push_u64(ca, arm64_native.arm64_rec_mov("x29", "sp") as u64);
    cfi_word(ca, 4, 29, 0);
    cfi_word(ca, 7, 0, 0);
    buf_push_u64(ca, arm64_native.arm64_rec_pair(true, 1, "x29", "x30", "sp", 16) as u64);
    cfi_word(ca, 5, 31, 0);
    buf_push_u64(ca, arm64_native.arm64_rec_ret() as u64);
    cfi_word(ca, 8, 0, 0);
    cfi_word(ca, 2, 0, 0);
    let ar = arm64_native.arm64_gas_program_words(".text\nf:\n\x06\n\x01\n\x06\n\x06\n\x06\n\x01\n\x06\n\x06\n\x01\n\x06\n\x01\n\x06\n\x06\n", buf_take_bytes(ca));
    let at = arm64_native.arm64_gas_program(".text\nf:\n    .cfi_startproc\n    stp x29, x30, [sp, #-16]!\n    .cfi_def_cfa_offset 16\n    .cfi_offset x29, -16\n    .cfi_offset x30, -8\n    mov x29, sp\n    .cfi_def_cfa_register x29\n    .cfi_remember_state\n    ldp x29, x30, [sp], #16\n    .cfi_def_cfa sp, 0\n    ret\n    .cfi_restore_state\n    .cfi_endproc\n");
    let aeh: i32[] = arm64_native.arm64_eh_frame(ar, 0 as i64, 0 as i64);
    print("arm64_cfi " + util.i32_to_string(first_diff(aeh, arm64_native.arm64_eh_frame(at, 0 as i64, 0 as i64))) + " " + util.i32_to_string(first_diff(ar.asm.code, at.asm.code)) + " " + util.i32_to_string(ar.unknown.len() + at.unknown.len()));
    print("arm64_cfi_eh " + util.i32_to_string(aeh.len()));
    // x86: a named record is the call or the rip-relative address its text
    // assembles to, forward and backward; one without its record, and one
    // naming a symbol nothing defines, are refused as the text is.
    let nw: usize = buf_new(64);
    bytes_into(nw, x86_native.x86_rec_call());
    bytes_into(nw, x86_native.x86_rec_lea_rip(7));
    bytes_into(nw, x86_native.x86_rec_movslq_rr(0, 0));
    bytes_into(nw, x86_native.x86_rec_ret());
    bytes_into(nw, x86_native.x86_rec_call());
    bytes_into(nw, x86_native.x86_rec_ret());
    let nr = x86_native.x86_gas_assemble_words(".text\nf:\n\x07g\n\x07g\n\x01\n\x01\ng:\n\x07f\n\x01\n", buf_take_bytes(nw));
    let nt = x86_native.x86_gas_assemble_words(".text\nf:\n    call g\n    leaq g(%rip), %rdi\n    movslq %eax, %rax\n    ret\ng:\n    call f\n    ret\n", buf_take_bytes(buf_new(1)));
    print("x86_named " + util.i32_to_string(first_diff(nr.code, nt.code)) + " " + util.i32_to_string(nr.unknown.len() + nt.unknown.len()) + " " + util.i32_to_string(nr.code.len()));
    let nm = x86_native.x86_gas_assemble_words(".text\n\x07g\n", buf_take_bytes(buf_new(1)));
    print("x86_named_missing " + util.i32_to_string(nm.unknown.len()));
    let nu = x86_native.x86_gas_assemble_words(".text\n\x07nowhere\n", words_of(x86_native.x86_rec_call()));
    let tu = x86_native.x86_gas_assemble_words(".text\n    call nowhere\n", buf_take_bytes(buf_new(1)));
    print("x86_named_undefined " + util.i32_to_string(nu.unknown.len() - tu.unknown.len()) + " " + util.i32_to_string(tu.unknown.len()));
    // x86: each record encoder of the frame, the stack and the memory forms
    // assembles to the bytes its text assembles to.
    rec_vs_text("x86_movslq", x86_native.x86_rec_movslq_rr(0, 3), "movslq %ebx, %rax");
    rec_vs_text("x86_movslq_hi", x86_native.x86_rec_movslq_rr(9, 10), "movslq %r10d, %r9");
    rec_vs_text("x86_push_r12", x86_native.x86_rec_push_r(12), "pushq %r12");
    rec_vs_text("x86_pop_rbp", x86_native.x86_rec_pop_r(5), "popq %rbp");
    rec_vs_text("x86_push_slot", x86_native.x86_rec_push_m(5, 0 - 24), "pushq -24(%rbp)");
    rec_vs_text("x86_ret", x86_native.x86_rec_ret(), "ret");
    rec_vs_text("x86_leave", x86_native.x86_rec_leave(), "leave");
    rec_vs_text("x86_lea_rsp", x86_native.x86_rec_lea_rm(4, 5, 0 - 16), "leaq -16(%rbp), %rsp");
    rec_vs_text("x86_mov_rmi_store", x86_native.x86_rec_mov_rmi(64, false, 6, 7, 2, 8, 8), "movq %rsi, 8(%rdi,%rdx,8)");
    rec_vs_text("x86_mov_rmi_byte", x86_native.x86_rec_mov_rmi(8, false, 6, 7, 2, 1, 8), "movb %sil, 8(%rdi,%rdx)");
    rec_vs_text("x86_mov_rmi_load", x86_native.x86_rec_mov_rmi(64, true, 12, 13, 1, 8, 8), "movq 8(%r13,%rcx,8), %r12");
    rec_vs_text("x86_movzb_mi", x86_native.x86_rec_movzb_mi(0, 2, 0, 1, 8), "movzbl 8(%rax,%rcx), %edx");
    rec_vs_text("x86_movzb_mi0", x86_native.x86_rec_movzb_mi(0, 9, 2, 1, 0), "movzbl (%rdx,%rcx), %r9d");
    rec_vs_text("x86_mov_ir32", x86_native.x86_rec_mov_ir(32, 0 - 1, 1), "movl $4294967295, %ecx");
    rec_vs_text("x86_mov_ir32_r9", x86_native.x86_rec_mov_ir(32, 7, 9), "movl $7, %r9d");
    rec_vs_text("x86_mov_ir64", x86_native.x86_rec_mov_ir(64, 0 - 1, 9), "movq $-1, %r9");
    rec_vs_text("x86_mov_im", x86_native.x86_rec_mov_im(64, 7, 5, 0 - 16), "movq $7, -16(%rbp)");
    rec_vs_text("x86_inc_r12", x86_native.x86_rec_inc_r(12), "incq %r12");
    rec_vs_text("x86_mov_rsp_base", x86_native.x86_rec_mov_rm(64, true, 0, 4, 0), "movq (%rsp), %rax");
    rec_vs_text("x86_mov_r13_base", x86_native.x86_rec_mov_rm(64, true, 0, 13, 0), "movq (%r13), %rax");
    rec_vs_text("x86_alu_rsp", x86_native.x86_rec_alu_ir(0, 64, 8, 4), "addq $8, %rsp");
    // x86: consecutive records are one run, laid down as their text would be,
    // and a label id between two records closes one run and starts another.
    let run: usize = buf_new(64);
    for x in words_of(x86_native.x86_rec_push_r(12)) { buf_push_byte(run, x as i32); }
    for x in words_of(x86_native.x86_rec_mov_rr(64, 5, 4)) { buf_push_byte(run, x as i32); }
    buf_push_u64(run, 3 as u64);
    for x in words_of(x86_native.x86_rec_pop_r(12)) { buf_push_byte(run, x as i32); }
    let rr = x86_native.x86_gas_assemble_words(".text\n\x01\n\x01\n\x04\n\x01\n", buf_take_bytes(run));
    let rt = x86_native.x86_gas_assemble_words(".text\n    pushq %r12\n    movq %rsp, %rbp\nl:\n    popq %r12\n", buf_take_bytes(buf_new(1)));
    print("x86_run " + util.i32_to_string(first_diff(rr.code, rt.code)) + " " + util.i32_to_string(rr.unknown.len() + rt.unknown.len()) + " " + util.i32_to_string(rr.code.len()));
    // x86: a run the source ends in, its last marker without a newline, is
    // still laid down.
    let trun: usize = buf_new(64);
    for x in words_of(x86_native.x86_rec_push_r(12)) { buf_push_byte(trun, x as i32); }
    for x in words_of(x86_native.x86_rec_mov_rr(64, 5, 4)) { buf_push_byte(trun, x as i32); }
    let tail_run = x86_native.x86_gas_assemble_words(".text\n\x01\n\x01", buf_take_bytes(trun));
    print("x86_run_tail " + util.i32_to_string(tail_run.unknown.len()) + " " + util.i32_to_string(tail_run.code.len()));
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
    // arm64: the sxtw record is the word its text assembles to, and refuses
    // a w destination or an x source.
    let sxw: usize = buf_new(16);
    buf_push_u64(sxw, arm64_native.arm64_rec_sxtw("x9", "w11") as u64);
    let sxr = arm64_native.arm64_gas_program_words(".text\n\x01\n", buf_take_bytes(sxw));
    let sxt = arm64_native.arm64_gas_program(".text\n    sxtw x9, w11\n");
    print("arm64_sxtw " + util.i32_to_string(first_diff(sxr.asm.code, sxt.asm.code)) + " " + util.i32_to_string(sxr.unknown.len() + sxt.unknown.len()) + " " + util.i32_to_string(sxr.asm.code.len()));
    print("arm64_sxtw_bad " + util.i64_to_string(arm64_native.arm64_rec_sxtw("w9", "w11")) + " " + util.i64_to_string(arm64_native.arm64_rec_sxtw("x9", "x11")) + " " + util.i64_to_string(arm64_native.arm64_rec_sxtw("sp", "w0")));
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
	for _, name := range []string{"x86_cfi_eh", "arm64_cfi_eh"} {
		m := regexp.MustCompile(`(?m)^` + name + ` (\d+)`).FindSubmatch(out)
		if m == nil {
			t.Fatalf("no %q line in driver output:\n%s", name, out)
		}
		// A CIE and one FDE with its rules: an empty image would compare
		// equal and prove nothing.
		if n, _ := strconv.Atoi(string(m[1])); n < 40 {
			t.Errorf("%s renders only %d bytes of .eh_frame:\n%s", name, n, out)
		}
	}
	for _, want := range []string{
		"\nx86_named -1 0 22\n", // e8 rel32, 48 8d 3d rel32, 48 63 c0, c3, e8 rel32, c3
		"\nx86_named_missing 1\n",
		"\nx86_named_undefined 0 1\n",
		"\nx86_cfi -1 -1 0\n",
		"\nx86_cfi_bad 1\n",
		"\narm64_cfi -1 -1 0\n",
		"\nx86_movslq -1 0\n",
		"\nx86_movslq_hi -1 0\n",
		"\nx86_push_r12 -1 0\n",
		"\nx86_pop_rbp -1 0\n",
		"\nx86_push_slot -1 0\n",
		"\nx86_ret -1 0\n",
		"\nx86_leave -1 0\n",
		"\nx86_lea_rsp -1 0\n",
		"\nx86_mov_rmi_store -1 0\n",
		"\nx86_mov_rmi_byte -1 0\n",
		"\nx86_mov_rmi_load -1 0\n",
		"\nx86_movzb_mi -1 0\n",
		"\nx86_movzb_mi0 -1 0\n",
		"\nx86_mov_ir32 -1 0\n",
		"\nx86_mov_ir32_r9 -1 0\n",
		"\nx86_mov_ir64 -1 0\n",
		"\nx86_mov_im -1 0\n",
		"\nx86_inc_r12 -1 0\n",
		"\nx86_mov_rsp_base -1 0\n",
		"\nx86_mov_r13_base -1 0\n",
		"\nx86_alu_rsp -1 0\n",
		"\nx86_run -1 0 7\n",   // 41 54, 48 89 e5, 41 5c
		"\nx86_run_tail 0 5\n", // 41 54, 48 89 e5: the run closed by the end of the source
		"\nx86_undefined_unknown 1\n",
		"\n  branch to a label id nothing defines\n",
		"\nx86_defined_unknown 0\n",
		"\nx86_defined_code 2\n", // jmp to the next byte is EB 00
		"\narm64_sxtw -1 0 4\n",
		"\narm64_sxtw_bad -1 -1 -1\n",
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
