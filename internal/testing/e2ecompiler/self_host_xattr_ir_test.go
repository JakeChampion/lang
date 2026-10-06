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

// getxattr and lgetxattr (#9098), setxattr and lsetxattr (#9154) through the
// self-host IR path. All four are generated Fern runtime bodies
// (`asmcore.rt_src_getxattr`, `rt_src_setxattr`): Linux's two calls each, or
// Darwin's one with XATTR_NOFOLLOW in its options word for the `l` form. The
// host sets the attribute the reads start from, so the probe reads back a
// value it did not write itself, and the symlink tells each pair apart.

func selfHostXattrSource(t *testing.T, dir string) string {
	t.Helper()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	selfHostSetUserXattr(t, f, "user.fern", "hello\x00world")
	if err := os.Symlink("f", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	p := func(name string) string { return filepath.Join(dir, name) }
	return fmt.Sprintf(`function main(): i32 {
    match (getxattr(%[1]q, "user.fern")) {
        Ok(v) => {
            if (v.len() != 11) { return 1; }
            if (slice_unchecked(v, 6, 11) != "world") { return 2; }
        },
        Err(_) => { return 3; }
    }
    match (getxattr(%[2]q, "user.fern")) { Ok(v) => { if (v.len() != 11) { return 4; } }, Err(_) => { return 5; } }
    match (lgetxattr(%[2]q, "user.fern")) { Ok(_) => { return 6; }, Err(_) => {} }
    match (getxattr(%[1]q, "user.absent")) { Ok(_) => { return 7; }, Err(Other(_, m)) => { if (m != %[4]q) { return 8; } }, Err(_) => { return 9; } }
    match (lgetxattr(%[3]q, "user.fern")) { Ok(_) => { return 10; }, Err(NotFound(_)) => {}, Err(_) => { return 11; } }
    match (setxattr(%[1]q, "user.set", "a" + "\x00" + "b")) { Ok(_) => {}, Err(_) => { return 12; } }
    match (getxattr(%[1]q, "user.set")) { Ok(v) => { if (v.len() != 3 || v[1] != 0 as u8) { return 13; } }, Err(_) => { return 14; } }
    match (setxattr(%[2]q, "user.via", "v")) { Ok(_) => {}, Err(_) => { return 15; } }
    match (getxattr(%[1]q, "user.via")) { Ok(v) => { if (v != "v") { return 16; } }, Err(_) => { return 17; } }
    match (lsetxattr(%[2]q, "user.link", "v")) { Ok(_) => {}, Err(_) => {} }
    match (getxattr(%[1]q, "user.link")) { Ok(_) => { return 18; }, Err(_) => {} }
    match (setxattr(%[3]q, "user.set", "v")) { Ok(_) => { return 19; }, Err(NotFound(_)) => {}, Err(_) => { return 20; } }
    return 0;
}
`, p("f"), p("l"), p("missing"), selfHostXattrAbsentText)
}

func TestSelfHostXattrIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("xattr test runs only natively (reads host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostXattrSource(t, t.TempDir())))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	for _, sym := range []string{"__fern_getxattr", "__fern_lgetxattr", "__fern_setxattr", "__fern_lsetxattr"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("%s did not reach the IR runtime path (absent from the asm)", sym)
		}
	}
	progBin := buildBin(t, gcc, dir, "xattr_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the step (see selfHostXattrSource)", code)
	}
}

func TestSelfHostXattrIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("xattr test runs only natively (reads host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostXattrSource(t, t.TempDir())))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, driverStderr(err))
	}
	for _, sym := range []string{"bl __fn___fern_getxattr", "bl __fn___fern_lgetxattr"} {
		if !bytes.Contains(asm, []byte(sym)) {
			t.Fatalf("no `%s` in the emitted asm — it did not lower through the arm64 IR path", sym)
		}
	}
	bin := buildBinArm64(t, arm64gcc, dir, "xattr_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("arm64 program exited %d, want 0 — the code names the step (see selfHostXattrSource)", code)
	}
}

// Darwin's getxattr takes a position and an options word; XATTR_NOFOLLOW is
// its lgetxattr, so a wrong word reads the target through the link.
func TestSelfHostArm64DarwinXattr(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	src := filepath.Join(dir, "xattr.fern")
	if err := os.WriteFile(src, []byte(selfHostXattrSource(t, t.TempDir())), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "xattr")
	if out, err := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, stdlib).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	run := exec.Command(bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("program exited %d, want 0 — the code names the step (see selfHostXattrSource)", code)
	}
}
