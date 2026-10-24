package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostArm64Labels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 labels e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64LabelsSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64 labels self-test")
	}
	watPath := filepath.Join(dir, "arm64_labels_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 labels self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

func TestSelfHostArm64DarwinMachOCallRuns(t *testing.T) {
	assertMachORuns(t, machoRun{name: "call42", main: arm64MachOCallDriverMain, wantExit: 42})
}

const arm64LabelsSelfTestMain = `
function main(): i32 {
    // forward b: b skip; <movz>; skip: -> b at off 0 targets off 8, rel +8
    // -> 0x14000002 -> 02 00 00 14
    let a: Arm64Asm = arm64_asm_new();
    a = arm64_asm_b(a, "skip");
    arm64_push_arr(a.code, arm64_movz([], arm64_x0(), 99, 0, false));
    a = arm64_asm_label(a, "skip");
    a = arm64_asm_resolve(a);
    if (a.text[0] != 2 || a.text[1] != 0 || a.text[2] != 0 || a.text[3] != 20) { return 1; }

    // forward b.eq: b.eq end; <movz>; end: -> at off 0 targets off 8 ->
    // 0x54000040 -> 40 00 00 54
    let b: Arm64Asm = arm64_asm_new();
    b = arm64_asm_bcond(b, arm64_eq(), "end");
    arm64_push_arr(b.code, arm64_movz([], arm64_x0(), 7, 0, false));
    b = arm64_asm_label(b, "end");
    b = arm64_asm_resolve(b);
    if (b.text[0] != 64 || b.text[1] != 0 || b.text[2] != 0 || b.text[3] != 84) { return 2; }

    // forward bl: bl f; f: -> bl at off 0 targets off 4, rel +4 ->
    // 0x94000001 -> 01 00 00 94
    let c: Arm64Asm = arm64_asm_new();
    c = arm64_asm_bl(c, "f");
    c = arm64_asm_label(c, "f");
    c = arm64_asm_resolve(c);
    if (c.text[0] != 1 || c.text[1] != 0 || c.text[2] != 0 || c.text[3] != 148) { return 3; }

    // backward cbnz: top: <movz>; cbnz x1, top -> cbnz at off 4 targets
    // off 0, rel -4 (known when queued) -> 0xB5FFFFE1 -> E1 FF FF B5
    let d: Arm64Asm = arm64_asm_new();
    d = arm64_asm_label(d, "top");
    arm64_push_arr(d.code, arm64_movz([], arm64_x0(), 1, 0, false));
    d = arm64_asm_cbnz(d, arm64_x1(), "top", false);
    d = arm64_asm_resolve(d);
    if (d.text[4] != 225 || d.text[5] != 255 || d.text[6] != 255 || d.text[7] != 181) { return 4; }

    // label lookup: unknown -> -1; placed -> its offset.
    if (arm64_asm_label_off(d, "nope") != (0 - 1)) { return 5; }
    if (arm64_asm_label_off(d, "top") != 0) { return 6; }
    return 0;
}
`

const arm64MachOCallDriverMain = `
function main(): i32 {
    let a: Arm64Asm = arm64_asm_new();
    a = arm64_asm_bl(a, "compute");                    // call compute (forward)
    arm64_push_arr(a.code, arm64_movz([], arm64_x16(), 1, 0, false));    // SYS_exit (Darwin)
    arm64_push_arr(a.code, arm64_svc([], 128));                    // svc #0x80
    a = arm64_asm_label(a, "compute");
    arm64_push_arr(a.code, arm64_movz([], arm64_x0(), 0, 0, false));     // acc = 0
    arm64_push_arr(a.code, arm64_movz([], arm64_x1(), 7, 0, false));     // counter = 7
    a = arm64_asm_label(a, "loop");
    arm64_push_arr(a.code, arm64_addimm([], arm64_x0(), arm64_x0(), 6, false)); // acc += 6
    arm64_push_arr(a.code, arm64_subimm([], arm64_x1(), arm64_x1(), 1, false)); // counter -= 1
    a = arm64_asm_cbnz(a, arm64_x1(), "loop", false);          // loop if != 0 (backward)
    arm64_push_arr(a.code, arm64_ret([], arm64_lr()));             // return to caller
    a = arm64_asm_resolve(a);
    let none: i32[] = [];
    let bin: i32[] = macho_executable(a.text, none, none, "fern", macho_entry_off(a), 0, none);
    write(string_from_bytes_unchecked(to_u8(bin)));
    return 0;
}
`

func TestSelfHostArm64GasNumericLabels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 numeric-label e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64NumericLabelSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the numeric-label self-test")
	}
	watPath := filepath.Join(dir, "arm64_numlabel_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 numeric-label self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64NumericLabelSelfTestMain = `
function main(): i32 {
    let src: string = "";
    src = src + "    b.ne 1f\n";
    src = src + "1:\n";
    src = src + "    mov x0, #0\n";
    src = src + "    b.eq 1f\n";
    src = src + "    mov x0, #0\n";
    src = src + "1:\n";
    src = src + "    mov x0, #0\n";
    src = src + "    b 1b\n";

    let p: Arm64GasProg = arm64_gas_program(src);
    // Every reference resolved, so nothing is refused.
    if (p.unknown.len() != 0) { return 1; }
    let a: Arm64Asm = p.asm;
    if (a.text.len() != 24) { return 2; }

    // b.ne 1f @0 -> the FIRST definition (off 4): imm19 = (4 - 0) / 4 = 1.
    if (imm19_at(a.text, 0) != 1) { return 3; }
    // b.eq 1f @8 -> the SECOND definition (off 16), NOT the first: imm19 =
    // (16 - 8) / 4 = 2. This is the check the old digit-keyed lookup could
    // never pass — it had one label named "1".
    if (imm19_at(a.text, 8) != 2) { return 4; }
    // b 1b @20 -> the most recent definition (off 16): rel = -4, so the
    // 26-bit field holds -1 as 0x3ffffff.
    if (imm26_at(a.text, 20) != 67108863) { return 5; }

    // An undefined target is REFUSED, not patched to a wild offset.
    let p2: Arm64GasProg = arm64_gas_program("    b nowhere\n");
    if (p2.unknown.len() != 1) { return 6; }
    // A numeric reference with no matching definition is refused the same way.
    let p3: Arm64GasProg = arm64_gas_program("    b.eq 1f\n    mov x0, #0\n");
    if (p3.unknown.len() != 1) { return 7; }
    return 0;
}

// imm19_at extracts the 19-bit branch offset (in instructions) from the
// conditional-branch word at byte offset at. Bits 5..23 sit inside the low
// three bytes, so this needs no 32-bit assembly.
function imm19_at(code: u8[], at: i32): i32 {
    let lo: i32 = code[at] as i32 + code[at + 1] as i32 * 256 + code[at + 2] as i32 * 65536;
    return (lo >> 5) & 524287;
}

// imm26_at extracts the 26-bit offset from an unconditional-branch word.
function imm26_at(code: u8[], at: i32): i32 {
    let w: i32 = code[at] as i32 + code[at + 1] as i32 * 256 + code[at + 2] as i32 * 65536 + (code[at + 3] as i32 & 3) * 16777216;
    return w & 67108863;
}

`

func TestSelfHostArm64BranchRange(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 branch-range e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64BranchRangeSelfTestMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the branch-range self-test")
	}
	watPath := filepath.Join(dir, "arm64_branchrange_selftest.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", watPath)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("arm64 branch-range self-test failed at check %d\n--- WAT ---\n%s", code, wat)
	}
}

const arm64BranchRangeSelfTestMain = `
function main(): i32 {
    // ---- imm26: field holds rel/4, so the reach is +/- (1 << 27) bytes ----
    if (!arm64_rel_fits(0, 26)) { return 1; }
    if (!arm64_rel_fits(4 * 33554431, 26)) { return 2; }        // (1<<25)-1 insns
    if (arm64_rel_fits(4 * 33554432, 26)) { return 3; }         // one past the top
    if (!arm64_rel_fits(0 - 4 * 33554432, 26)) { return 4; }    // -(1<<25) insns
    if (arm64_rel_fits(0 - 4 * 33554436, 26)) { return 5; }     // one past the bottom

    // ---- imm19: +/- 1 MB ----
    if (!arm64_rel_fits(4 * 262143, 19)) { return 6; }
    if (arm64_rel_fits(4 * 262144, 19)) { return 7; }
    if (!arm64_rel_fits(0 - 4 * 262144, 19)) { return 8; }
    if (arm64_rel_fits(0 - 4 * 262148, 19)) { return 9; }

    // ---- imm14: +/- 32 KB ----
    if (!arm64_rel_fits(4 * 8191, 14)) { return 10; }
    if (arm64_rel_fits(4 * 8192, 14)) { return 11; }
    if (!arm64_rel_fits(0 - 4 * 8192, 14)) { return 12; }
    if (arm64_rel_fits(0 - 4 * 8196, 14)) { return 13; }

    // A displacement that is not 4-aligned is unencodable at any width.
    if (arm64_rel_fits(2, 26)) { return 14; }
    if (arm64_rel_fits(0 - 1, 19)) { return 15; }

    // The kind -> width mapping the patch sites use.
    if (arm64_branch_field_bits(0) != 26) { return 16; }
    if (arm64_branch_field_bits(1) != 19) { return 17; }
    if (arm64_branch_field_bits(2) != 14) { return 18; }

    // ---- integration, BACKWARD (displacement known at once) ----
    // Place the label, put 8200 instructions between it and the tbz, and the
    // displacement (-32800) is past imm14's -32768.
    let a: Arm64Asm = arm64_asm_new();
    a = arm64_asm_label(a, "far");
    let i: i32 = 0;
    while (i < 8200) {
        arm64_push_arr(a.code, arm64_movz([], arm64_x0(), 0, 0, false));
        i = i + 1;
    }
    let before: i32 = buf_len(a.code);
    a = arm64_asm_tbz(a, arm64_x1(), 0, "far");
    if (a.oor.len() != 1) { return 19; }
    // The placeholder is still there (the instruction was emitted, not patched)
    // and neither a fixup nor a patch was queued, so nothing downstream will
    // encode it either.
    if (buf_len(a.code) != before + 4) { return 20; }
    if (arm64_fix_count(a) != 0 || a.patq != 0 as usize) { return 21; }

    // ---- integration, FORWARD (resolved later) ----
    let b: Arm64Asm = arm64_asm_new();
    b = arm64_asm_tbz(b, arm64_x1(), 0, "ahead");
    if (arm64_fix_count(b) != 1) { return 22; }   // queued, range not yet knowable
    let j: i32 = 0;
    while (j < 8200) {
        arm64_push_arr(b.code, arm64_movz([], arm64_x0(), 0, 0, false));
        j = j + 1;
    }
    b = arm64_asm_label(b, "ahead");
    if (b.oor.len() != 0) { return 23; }        // still unresolved at this point
    b = arm64_asm_resolve(b);
    if (b.oor.len() != 1) { return 24; }

    // An in-range branch is unaffected: same shape, 8 instructions apart.
    let c: Arm64Asm = arm64_asm_new();
    c = arm64_asm_label(c, "near");
    let k: i32 = 0;
    while (k < 8) {
        arm64_push_arr(c.code, arm64_movz([], arm64_x0(), 0, 0, false));
        k = k + 1;
    }
    c = arm64_asm_tbz(c, arm64_x1(), 0, "near");
    if (c.oor.len() != 0) { return 25; }
    return 0;
}

`
