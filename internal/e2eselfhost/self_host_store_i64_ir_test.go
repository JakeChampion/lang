package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// storeI64Src is a __store_i64 / __load_i64 round-trip witness. It stores a
// value whose high 32 bits are non-zero (5000000005 = 0x1_2A05F205) so a store
// that truncated to the low 32 bits (the store_ptr / store_i32 4-byte width)
// would read back 0x2A05F205 and fail the first check. A negative i64 covers the
// sign-bit path. Success returns 42. The address comes from __alloc (a raw usize
// bump-heap pointer, like core/map's packed-entry buffer users).
const storeI64Src = `function main(): i32 {
    var p: usize = __alloc(16);
    __store_i64(p, 5000000005 as i64);
    if (__load_i64(p) != 5000000005) { return 1; }
    __store_i64(p + 8, (0 - 3) as i64);
    if (__load_i64(p + 8) != (0 - 3)) { return 2; }
    return 42;
}`

// TestSelfHostStoreI64IRX86_64 pins __store_i64 on the self-host x86-64 IR path
// (#4375 item 2). irlower lowered __load_i64 / __store_i32 / __store_ptr but not
// __store_i64 — the store half of the 8-byte raw-memory pair was missing, so a
// program writing an i64 through a raw address bailed to the AST fallback (which
// lacks the __fn___store_i64 runtime helper) or mis-stored. __store_i64 now
// lowers to op_store_i64 (kind 199): the value routes through lower_i64 (8-byte)
// and the x86 backend emits an 8-byte movq (shared with store_ptr).
//
// Oracle is the NATIVE x86-64 compiler, which implements __store_i64
// (x86_64.go): a truncating store would diverge from native's full-width one, so
// IR == native also proves the program took the IR path, not the AST fallback.
func TestSelfHostStoreI64IRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(storeI64Src + "\n"))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "store_i64", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("store_i64 IR binary did not exit normally (segfault?)")
	}
	_, want := compileAndRunX86_64(t, storeI64Src+"\n") // native = the correct oracle
	if got := run.ProcessState.ExitCode(); got != want {
		t.Errorf("store_i64 IR exit = %d, want %d (native); a truncating store would give a different value", got, want)
	}
}

// TestSelfHostStoreI64IRArm64 runs the same witness through the self-host arm64
// IR path (asm_ir_run -target arm64-linux -ir) under qemu (#4375 item 2). Unlike x86,
// the NATIVE arm64 compiler does not implement __store_i64 (it emits an
// "undefined label __store_i64"), so — like udp_send on arm64 (item 3) — the
// self-host arm64 IR path is intentionally AHEAD of native here. The oracle is
// therefore self-consistency: the store/load round-trip must recover the exact
// 64-bit value (exit 42), which a 32-bit-truncating store would fail. On arm64
// __store_i64 shares store_ptr's 8-byte `str x1` arm. CI-gated arm64 (qemu).
func TestSelfHostStoreI64IRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern",
		"asm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(x86runner) == 0 {
		cmd = exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	} else {
		cmd = exec.Command(x86runner[0], append(append(append([]string{}, x86runner[1:]...), driverBin), "-target", "arm64-linux", "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(storeI64Src + "\n"))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__store_i64")) && !bytes.Contains(asm, []byte("str x1")) {
		// The op lowered inline (no runtime label), so just assert we got asm.
		t.Logf("note: store_i64 lowered inline (expected)")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "store_i64_arm64", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatal("store_i64 arm64 IR binary did not exit normally (segfault?)")
	}
	if got := run.ProcessState.ExitCode(); got != 42 {
		t.Errorf("store_i64 arm64 IR exit = %d, want 42 (full 64-bit round-trip; a truncating store would differ)", got)
	}
}
