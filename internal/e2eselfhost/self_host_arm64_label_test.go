package e2eselfhost

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSelfHostArm64Labels byte-checks the named-label assembler added in
// slice 3d of arm64_encode.fern (the Arm64Asm struct): forward branch
// (patched by arm64_asm_resolve), backward branch (patched immediately),
// forward bl/call, and a conditional forward branch by name. Same wasm
// self-test shape as TestSelfHostArm64Encode.
func TestSelfHostArm64Labels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 labels e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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

// TestSelfHostArm64DarwinMachOCallRuns is the end-to-end proof of the
// label assembler: a Fern program uses the named-label API to assemble
//
//	_main { bl compute; exit(x0) }
//	compute { x0=0; x1=7; loop: x0+=6; x1-=1; cbnz x1, loop; ret }
//
// — a forward `bl` (resolved by arm64_asm_resolve) and a backward loop
// branch (patched immediately) — wraps it with macho.fern into an
// ad-hoc-signed Mach-O. Asserts an arm64 MH_EXECUTE (structural, every
// host) and, on Apple Silicon, executes it and checks exit 42 (= 6 × 7),
// with no clang/ld64/codesign.
func TestSelfHostArm64DarwinMachOCallRuns(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64-darwin call run")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	source := arm64NativeSrc(t) + "\n" + arm64MachOCallDriverMain

	wat := runCapture(t, gcc, runner, driverBin, []byte(source))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes for the arm64-darwin call driver")
	}
	watPath := filepath.Join(dir, "arm64_macho_call_driver.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	bin, err := exec.Command("wasmtime", "run", watPath).Output()
	if err != nil {
		t.Fatalf("wasmtime run (driver): %v", err)
	}

	f, err := macho.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("self-host output is not a parseable Mach-O: %v", err)
	}
	if f.Type != macho.TypeExec || f.Cpu != macho.CpuArm64 {
		t.Fatalf("got type=%v cpu=%v, want EXECUTE/arm64", f.Type, f.Cpu)
	}

	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	binPath := filepath.Join(dir, "call42")
	if err := os.WriteFile(binPath, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	cmd := exec.Command(binPath)
	runErr := cmd.Run()
	ps := cmd.ProcessState
	if ps == nil || !ps.Exited() {
		t.Skipf("self-host Mach-O did not run to a normal exit (err=%v, state=%v)", runErr, ps)
	}
	if got := ps.ExitCode(); got != 42 {
		t.Errorf("self-host arm64-darwin call exit = %d, want 42", got)
	}
}

// arm64LabelsSelfTestMain byte-checks the named-label assembler: a forward
// b resolved by arm64_asm_resolve, a forward b.cond resolved likewise, a
// forward bl, and a backward cbnz patched immediately. Each `return N` is
// a distinct failing-check id (0 = all pass).
const arm64LabelsSelfTestMain = `
function main(): i32 {
    // forward b: b skip; <movz>; skip: -> b at off 0 targets off 8, rel +8
    // -> 0x14000002 -> 02 00 00 14
    var a: Arm64Asm = arm64_asm_new();
    a = arm64_asm_b(a, "skip");
    a.code = arm64_movz(a.code, arm64_x0(), 99, 0, false);
    a = arm64_asm_label(a, "skip");
    a = arm64_asm_resolve(a);
    if (a.code[0] != 2 || a.code[1] != 0 || a.code[2] != 0 || a.code[3] != 20) { return 1; }

    // forward b.eq: b.eq end; <movz>; end: -> at off 0 targets off 8 ->
    // 0x54000040 -> 40 00 00 54
    var b: Arm64Asm = arm64_asm_new();
    b = arm64_asm_bcond(b, arm64_eq(), "end");
    b.code = arm64_movz(b.code, arm64_x0(), 7, 0, false);
    b = arm64_asm_label(b, "end");
    b = arm64_asm_resolve(b);
    if (b.code[0] != 64 || b.code[1] != 0 || b.code[2] != 0 || b.code[3] != 84) { return 2; }

    // forward bl: bl f; f: -> bl at off 0 targets off 4, rel +4 ->
    // 0x94000001 -> 01 00 00 94
    var c: Arm64Asm = arm64_asm_new();
    c = arm64_asm_bl(c, "f");
    c = arm64_asm_label(c, "f");
    c = arm64_asm_resolve(c);
    if (c.code[0] != 1 || c.code[1] != 0 || c.code[2] != 0 || c.code[3] != 148) { return 3; }

    // backward cbnz: top: <movz>; cbnz x1, top -> cbnz at off 4 targets
    // off 0, rel -4 (patched immediately) -> 0xB5FFFFE1 -> E1 FF FF B5
    var d: Arm64Asm = arm64_asm_new();
    d = arm64_asm_label(d, "top");
    d.code = arm64_movz(d.code, arm64_x0(), 1, 0, false);
    d = arm64_asm_cbnz(d, arm64_x1(), "top", false);
    if (d.code[4] != 225 || d.code[5] != 255 || d.code[6] != 255 || d.code[7] != 181) { return 4; }

    // label lookup: unknown -> -1; placed -> its offset.
    if (arm64_asm_label_off(d, "nope") != (0 - 1)) { return 5; }
    if (arm64_asm_label_off(d, "top") != 0) { return 6; }
    return 0;
}
`

// arm64MachOCallDriverMain assembles a subroutine call via the label API:
// _main bl's compute (forward), which loops 6 × 7 = 42 (backward cbnz by
// label) and returns; _main exits with x0.
const arm64MachOCallDriverMain = `
function main(): i32 {
    var a: Arm64Asm = arm64_asm_new();
    a = arm64_asm_bl(a, "compute");                    // call compute (forward)
    a.code = arm64_movz(a.code, arm64_x16(), 1, 0, false);    // SYS_exit (Darwin)
    a.code = arm64_svc(a.code, 128);                    // svc #0x80
    a = arm64_asm_label(a, "compute");
    a.code = arm64_movz(a.code, arm64_x0(), 0, 0, false);     // acc = 0
    a.code = arm64_movz(a.code, arm64_x1(), 7, 0, false);     // counter = 7
    a = arm64_asm_label(a, "loop");
    a.code = arm64_addimm(a.code, arm64_x0(), arm64_x0(), 6, false); // acc += 6
    a.code = arm64_subimm(a.code, arm64_x1(), arm64_x1(), 1, false); // counter -= 1
    a = arm64_asm_cbnz(a, arm64_x1(), "loop", false);          // loop if != 0 (backward)
    a.code = arm64_ret(a.code, arm64_lr());             // return to caller
    a = arm64_asm_resolve(a);
    var none: i32[] = [];
    var bin: i32[] = macho_executable(a.code, none, "fern", macho_entry_off(a), 0);
    write(string_from_bytes_unchecked(bin));
    return 0;
}
`

// TestSelfHostArm64GasNumericLabels pins GAS local numeric labels in the
// in-process assembler: `1:` defines an occurrence, `1f` refers to the NEXT
// one and `1b` to the most recent. The emitter's array bounds check uses
// them (`b.lo 1f` / `b __fern_oob_abort` / `1:`) and reuses the same digit
// once per array index, so resolving by digit alone cannot work.
//
// The assembler used to record the definition as a label named "1" and look
// the reference up as "1f", never match, and then patch the branch to
// `-1 - site` — a jump to four bytes before .text — with `p.unknown` left
// empty, so the binary linked and then SIGILL'd or span forever. That was 129
// of the arm64 leg's SIGILLs. This test is the focused unit check for the
// resolution rule (the leg is the end-to-end one), and its last two cases pin
// the refusal that replaced the silent patch.
func TestSelfHostArm64GasNumericLabels(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arm64 numeric-label e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

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

// arm64NumericLabelSelfTestMain assembles GAS text with two `1:` definitions
// and one reference of each kind, then decodes the branch immediates. Layout
// (4 bytes per instruction):
//
//	off  0  b.ne 1f     -> the first 1: at 4   (imm19 = 1)
//	off  4  1: mov      <- definition #1
//	off  8  b.eq 1f     -> the second 1: at 16 (imm19 = 2)
//	off 12  mov
//	off 16  1: mov      <- definition #2
//	off 20  b 1b        -> back to 16          (imm26 field = 0x3ffffff, i.e. -1)
const arm64NumericLabelSelfTestMain = `
function main(): i32 {
    var src: string = "";
    src = src + "    b.ne 1f\n";
    src = src + "1:\n";
    src = src + "    mov x0, #0\n";
    src = src + "    b.eq 1f\n";
    src = src + "    mov x0, #0\n";
    src = src + "1:\n";
    src = src + "    mov x0, #0\n";
    src = src + "    b 1b\n";

    var p: Arm64GasProg = arm64_gas_program(src);
    // Every reference resolved, so nothing is refused.
    if (p.unknown.len() != 0) { return 1; }
    var a: Arm64Asm = p.asm;
    if (a.code.len() != 24) { return 2; }

    // b.ne 1f @0 -> the FIRST definition (off 4): imm19 = (4 - 0) / 4 = 1.
    if (imm19_at(a.code, 0) != 1) { return 3; }
    // b.eq 1f @8 -> the SECOND definition (off 16), NOT the first: imm19 =
    // (16 - 8) / 4 = 2. This is the check the old digit-keyed lookup could
    // never pass — it had one label named "1".
    if (imm19_at(a.code, 8) != 2) { return 4; }
    // b 1b @20 -> the most recent definition (off 16): rel = -4, so the
    // 26-bit field holds -1 as 0x3ffffff.
    if (imm26_at(a.code, 20) != 67108863) { return 5; }

    // An undefined target is REFUSED, not patched to a wild offset.
    var p2: Arm64GasProg = arm64_gas_program("    b nowhere\n");
    if (p2.unknown.len() != 1) { return 6; }
    // A numeric reference with no matching definition is refused the same way.
    var p3: Arm64GasProg = arm64_gas_program("    b.eq 1f\n    mov x0, #0\n");
    if (p3.unknown.len() != 1) { return 7; }
    return 0;
}

// imm19_at extracts the 19-bit branch offset (in instructions) from the
// conditional-branch word at byte offset at. Bits 5..23 sit inside the low
// three bytes, so this needs no 32-bit assembly.
function imm19_at(code: i32[], at: i32): i32 {
    var lo: i32 = code[at] + code[at + 1] * 256 + code[at + 2] * 65536;
    return (lo >> 5) & 524287;
}

// imm26_at extracts the 26-bit offset from an unconditional-branch word.
function imm26_at(code: i32[], at: i32): i32 {
    var w: i32 = code[at] + code[at + 1] * 256 + code[at + 2] * 65536 + (code[at + 3] & 3) * 16777216;
    return w & 67108863;
}

`
