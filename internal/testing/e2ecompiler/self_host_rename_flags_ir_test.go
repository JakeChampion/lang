package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// rename_noreplace and rename_exchange (#9784) through the self-host IR path.
// Both are generated Fern runtime bodies (`asmcore.rt_src_rename_flagged`),
// where `sysno(t, "renameat2")` and `rename_flag` carry the per-target
// difference: renameat2 on Linux, renameatx_np on Darwin with its own flag
// values. A wrong flag is still a plausible call, so the probe checks the
// resulting tree as well as each return value.

func selfHostRenameFlagsSource(dir string) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    match (write_file(%[1]q, "A")) { Ok(_) => {}, Err(_) => { return 1; } }
    match (write_file(%[2]q, "B")) { Ok(_) => {}, Err(_) => { return 2; } }
    match (rename_noreplace(%[1]q, %[2]q)) { Ok(_) => { return 3; }, Err(AlreadyExists(_)) => {}, Err(_) => { return 4; } }
    match (rename_exchange(%[1]q, %[2]q)) { Ok(_) => {}, Err(_) => { return 5; } }
    match (read_file(%[1]q)) { Ok(s) => { if (s != "B") { return 6; } }, Err(_) => { return 7; } }
    match (read_file(%[2]q)) { Ok(s) => { if (s != "A") { return 8; } }, Err(_) => { return 9; } }
    match (rename_noreplace(%[1]q, %[3]q)) { Ok(_) => {}, Err(_) => { return 10; } }
    match (read_file(%[1]q)) { Ok(_) => { return 11; }, Err(_) => {} }
    match (rename_exchange(%[3]q, %[4]q)) { Ok(_) => { return 12; }, Err(NotFound(_)) => {}, Err(_) => { return 13; } }
    return 0;
}
`, p("a"), p("b"), p("c"), p("missing"))
}

func selfHostRenameFlagsTree(t *testing.T, dir string) {
	t.Helper()
	for _, absent := range []string{"a", "missing"} {
		if _, err := os.Lstat(filepath.Join(dir, absent)); !os.IsNotExist(err) {
			t.Errorf("%s exists after the probe (lstat err = %v)", absent, err)
		}
	}
	for name, want := range map[string]string{"b": "A", "c": "B"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestSelfHostRenameFlagsIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("rename flags test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(selfHostRenameFlagsSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	for _, sym := range []string{"__fern_rename_noreplace", "__fern_rename_exchange"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("%s did not reach the IR runtime path (absent from the asm)", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "rename_flags_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the step (see selfHostRenameFlagsSource)", code)
	}
	selfHostRenameFlagsTree(t, work)
}

func TestSelfHostRenameFlagsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("rename flags test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	cmd := exec.Command(driverBin, "-target", "arm64-linux")
	cmd.Stdin = bytes.NewReader([]byte(selfHostRenameFlagsSource(work)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	for _, sym := range []string{"bl __fn___fern_rename_noreplace", "bl __fn___fern_rename_exchange"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("no `%s` in the emitted asm — it did not lower through the arm64 IR path", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "rename_flags_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 program exited %d, want 0 — the code names the step (see selfHostRenameFlagsSource)", code)
	}
	selfHostRenameFlagsTree(t, work)
}

// Darwin's renameatx_np takes RENAME_EXCL 4 where Linux's RENAME_NOREPLACE
// is 1, so the Linux word would ask XNU for something else.
func TestSelfHostArm64DarwinRenameFlags(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	work := t.TempDir()
	src := filepath.Join(dir, "rename_flags.fern")
	if err := os.WriteFile(src, []byte(selfHostRenameFlagsSource(work)), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "rename_flags")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the step (see selfHostRenameFlagsSource)", code)
	}
	selfHostRenameFlagsTree(t, work)
}
