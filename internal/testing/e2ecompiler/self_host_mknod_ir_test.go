package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

const (
	shMknodFifo    = 0o010666
	shMknodChr     = 0o020600
	shMknodMaj     = 1
	shMknodMin     = 256
	shMknodWideMaj = 4095
	shMknodWideMin = 1048575
)

func shLinuxRdev(major, minor uint64) uint64 {
	return minor&0xff | (major&0xfff)<<8 | (minor&0xfff00)<<12
}

func selfHostMknodSource(dir string, withDev bool) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	src := fmt.Sprintf(`function main(): i32 {
    umask(0);
    match (mknod(%[1]q, %[2]d, 0, 0)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (lstat(%[1]q)) {
        Ok(f) => {
            if ((f.mode as i64) != (%[2]d as i64)) { return 2; }
            if (f.is_file) { return 3; }
            if (f.is_dir) { return 4; }
        },
        Err(_) => { return 5; }
    }
    // A second creation on the same name is an Err, not a silent replace.
    match (mknod(%[1]q, %[2]d, 0, 0)) { Ok(_) => { return 6; }, Err(_) => {} }
    // A missing parent names the kind rather than answering a silent Ok.
    match (mknod(%[3]q, %[2]d, 0, 0)) {
        Ok(_) => { return 7; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 8; } } }
    }
`, p("fifo"), shMknodFifo, p("nodir/fifo"))
	if withDev {
		src += fmt.Sprintf(`    // A minor that does NOT fit in a byte: Linux splits it around the
    // major, so the legacy 8+8 packing lands a different node here.
    match (mknod(%[1]q, %[2]d, %[3]d, %[4]d)) { Ok(_) => {}, Err(_) => { return 9; } }
    match (lstat(%[1]q)) { Ok(f) => { if ((f.mode as i64) != (%[2]d as i64)) { return 10; } }, Err(_) => { return 11; } }
    // Past the field widths the packing has room for. The kernel ignores
    // every bit of dev above 31, so this has to be refused here or it
    // lands on a different, valid node.
    match (mknod(%[5]q, %[2]d, 4096, 1)) { Ok(_) => { return 12; }, Err(_) => {} }
    // Every bit of both fields. The major's is twelve wide, and nothing
    // above says so.
    match (mknod(%[6]q, %[2]d, %[7]d, %[8]d)) { Ok(_) => {}, Err(_) => { return 13; } }
`, p("big"), shMknodChr, shMknodMaj, shMknodMin, p("toobig"),
			p("wide"), shMknodWideMaj, shMknodWideMin)
	} else {
		src += fmt.Sprintf(`    // This process may not create a device node, so what the probe can
    // show is the refusal, and that nothing was left behind.
    match (mknod(%[1]q, %[2]d, %[3]d, %[4]d)) { Ok(_) => { return 9; }, Err(_) => {} }
    match (lstat(%[1]q)) { Ok(_) => { return 10; }, Err(_) => {} }
`, p("big"), shMknodChr, shMknodMaj, shMknodMin)
	}
	src += `    return 0;
}
`
	return src
}

func selfHostMknodTree(t *testing.T, dir string, withDev bool) {
	t.Helper()
	var fifo syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, "fifo"), &fifo); err != nil {
		t.Fatalf("lstat fifo: %v", err)
	}
	if fifo.Mode != shMknodFifo {
		t.Errorf("fifo mode = %o, want %o", fifo.Mode, shMknodFifo)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nodir")); !os.IsNotExist(err) {
		t.Errorf("nodir exists (lstat err = %v) — mknod created a parent", err)
	}
	if !withDev {
		if _, err := os.Lstat(filepath.Join(dir, "big")); !os.IsNotExist(err) {
			t.Errorf("big exists after a refused mknod (lstat err = %v)", err)
		}
		return
	}
	var big syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, "big"), &big); err != nil {
		t.Fatalf("lstat big: %v", err)
	}
	if want := shLinuxRdev(shMknodMaj, shMknodMin); uint64(big.Rdev) != want {
		t.Errorf("big rdev = %#x, want %#x — the minor was not split around the major "+
			"(the legacy 8+8 layout would answer %#x)",
			big.Rdev, want, shMknodMaj<<8|shMknodMin&0xff)
	}
	var wide syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, "wide"), &wide); err != nil {
		t.Fatalf("lstat wide: %v", err)
	}
	if want := shLinuxRdev(shMknodWideMaj, shMknodWideMin); uint64(wide.Rdev) != want {
		t.Errorf("wide rdev = %#x, want %#x — the major's field is not twelve bits wide",
			wide.Rdev, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "toobig")); !os.IsNotExist(err) {
		t.Errorf("toobig exists (lstat err = %v) — an out-of-range pair was accepted", err)
	}
}

func selfHostMknodDevAllowed(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, ".devprobe")
	err := syscall.Mknod(probe, syscall.S_IFCHR|0o600, int(shLinuxRdev(1, 3)))
	if err == nil {
		if rerr := os.Remove(probe); rerr != nil {
			t.Fatalf("remove device probe: %v", rerr)
		}
		return true
	}
	if err == syscall.EPERM || err == syscall.EACCES || err == syscall.EOPNOTSUPP || err == syscall.ENOSYS {
		t.Logf("device nodes are not creatable here (%v); the probe asserts the refusal instead", err)
		return false
	}
	t.Fatalf("device-node probe failed for a reason that is not a privilege one: %v", err)
	return false
}

func TestSelfHostMknodIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("mknod test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	dev := selfHostMknodDevAllowed(t, work)
	cmd := exec.Command(driverBin)
	cmd.Stdin = bytes.NewReader([]byte(selfHostMknodSource(work, dev)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_mknod")) {
		t.Fatal("mknod did not reach the IR runtime path (no __fern_mknod in the asm)")
	}
	progBin := buildBin(t, gcc, dir, "mknod_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("mknod program exited %d, want 0 — the code names the step (see selfHostMknodSource)", code)
	}
	selfHostMknodTree(t, work, dev)
}

func TestSelfHostMknodIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("mknod test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	work := t.TempDir()
	dev := selfHostMknodDevAllowed(t, work)
	cmd := exec.Command(driverBin, "-target", "arm64-linux")
	cmd.Stdin = bytes.NewReader([]byte(selfHostMknodSource(work, dev)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_mknod")) {
		t.Fatal("no `bl __fn___fern_mknod` in the emitted asm — it did not lower through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "mknod_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("mknod arm64 program exited %d, want 0 — the code names the step (see selfHostMknodSource)", code)
	}
	selfHostMknodTree(t, work, dev)
}
