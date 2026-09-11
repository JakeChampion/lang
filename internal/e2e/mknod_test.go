// `mknod` end to end on every backend that provides it — the four
// natives. Neither WASI preview can create an entry that is neither a
// file nor a directory, so E066 refuses the builtin there (capability
// `fsnode`) and the last test in this file is that refusal rather than a
// wasm probe.
//
// What no unit test can assert: each backend packs the major / minor pair
// into the kernel's dev_t by hand — three hand-written assembly sequences
// and one Go one — and Linux SPLITS the minor around the major. The
// legacy 8+8 layout agrees with the real one for every pair below 256, so
// a wrong packing is invisible until a minor exceeds 255, which is why
// the probe creates a (1, 256) node and reads its raw st_rdev back
// through Go's own Lstat rather than through Fern's `stat`.
//
// PRIVILEGE. A FIFO needs none. A character or block node generally needs
// CAP_MKNOD, so the harness MEASURES what it can do rather than assuming:
// the Go side tries `syscall.Mknod` itself in the same directory, and the
// probe then either asserts the node was created with the exact rdev or
// asserts the same refusal. There is no arm that silently skips.
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// The three S_IFMT types the builtin reaches, as the raw st_mode word
// `stat` reports: S_IFIFO 0o010000, S_IFCHR 0o020000, S_IFBLK 0o060000.
const (
	mknodFifo0666 = 0o010666
	mknodChr0600  = 0o020600
	mknodBlk0600  = 0o060600
)

// The device pairs. (12, 34) fits the legacy 8+8 layout; (1, 256) does
// not, and is the one that tells the two apart.
const (
	mknodChrMajor = 12
	mknodChrMinor = 34
	mknodBigMajor = 1
	mknodBigMinor = 256
	// Every bit of both fields: the widest pair the encoding holds, and
	// the only one that exercises the major above 8 bits. Without it a
	// major narrowed to a byte passes every other case here.
	mknodWideMajor = 4095
	mknodWideMinor = 1048575
)

// linuxRdev is the dev_t Linux packs a pair into, measured by creating
// nodes with mknod(1) and reading st_rdev back: minor[7:0], then
// major[11:0] from bit 8, then minor[19:8] from bit 20.
func linuxRdev(major, minor uint64) uint64 {
	return minor&0xff | (major&0xfff)<<8 | (minor&0xfff00)<<12
}

// mknodSource is the probe. `withDev` says whether the harness has
// established that this process may create a device node; when it is
// false the probe asserts the REFUSAL instead of dropping the case.
//
// Every failure returns its own exit code, so the number names the step.
func mknodSource(dir string, withDev bool) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	src := fmt.Sprintf(`function main(): i32 {
    // The mask is set here rather than inherited, so what it filters is
    // the same number on every runner.
    umask(0);
    match (mknod(%[1]q, %[2]d, 0, 0)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (lstat(%[1]q)) { Ok(st) => { if ((st.mode as i64) != (%[2]d as i64)) { return 2; } }, Err(_) => { return 3; } }
    // ...and a FIFO is not a file: stat has to say so, or every later
    // check is about something else.
    match (lstat(%[1]q)) { Ok(st) => { if (st.is_file) { return 4; } if (st.is_dir) { return 5; } }, Err(_) => { return 6; } }

    // The umask DOES apply, which is the whole difference from chmod:
    // this is a creation, and a mask is what filters one.
    umask(23);
    match (mknod(%[3]q, %[2]d, 0, 0)) { Ok(_) => {}, Err(_) => { return 7; } }
    match (lstat(%[3]q)) { Ok(st) => { if ((st.mode as i64) != (%[4]d as i64)) { return 8; } }, Err(_) => { return 9; } }
    umask(0);

    // An existing name is EEXIST and reaches the caller.
    match (mknod(%[1]q, %[2]d, 0, 0)) { Ok(_) => { return 10; }, Err(_) => {} }
    // ...and so is a missing parent directory.
    match (mknod(%[5]q, %[2]d, 0, 0)) {
        Ok(_) => { return 11; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 12; } } }
    }
`, p("fifo"), mknodFifo0666, p("masked"), mknodFifo0666&^0o027, p("nodir/fifo"))

	if withDev {
		src += fmt.Sprintf(`    // A character node, with a pair the legacy 8+8 dev_t layout also
    // fits — so on its own it proves nothing about the packing.
    match (mknod(%[1]q, %[2]d, %[3]d, %[4]d)) { Ok(_) => {}, Err(_) => { return 13; } }
    match (lstat(%[1]q)) { Ok(st) => { if ((st.mode as i64) != (%[2]d as i64)) { return 14; } }, Err(_) => { return 15; } }
    // A block node, which differs from the character one only in the
    // S_IFMT bits: a backend that masked the type away would make these
    // two the same entry.
    match (mknod(%[5]q, %[6]d, %[3]d, %[4]d)) { Ok(_) => {}, Err(_) => { return 16; } }
    match (lstat(%[5]q)) { Ok(st) => { if ((st.mode as i64) != (%[6]d as i64)) { return 17; } }, Err(_) => { return 18; } }
    // The minor that does NOT fit in a byte. Linux splits it around the
    // major, so a legacy 8+8 packing lands a different node here and
    // every check above still passes.
    match (mknod(%[7]q, %[2]d, %[8]d, %[9]d)) { Ok(_) => {}, Err(_) => { return 19; } }
    // Every bit of both fields. The major's is twelve wide, and nothing
    // above says so: a major narrowed to a byte packs the same word for
    // every other pair here.
    match (mknod(%[11]q, %[2]d, %[12]d, %[13]d)) { Ok(_) => {}, Err(_) => { return 22; } }
    // Past the field widths the packing has room for. The kernel ignores
    // every bit of dev above 31, so an out-of-range pair has to be
    // refused here or it lands on a different, valid node.
    match (mknod(%[10]q, %[2]d, 4096, 1)) { Ok(_) => { return 20; }, Err(_) => {} }
    match (mknod(%[10]q, %[2]d, 1, 1048576)) { Ok(_) => { return 21; }, Err(_) => {} }
`, p("chr"), mknodChr0600, mknodChrMajor, mknodChrMinor,
			p("blk"), mknodBlk0600, p("big"), mknodBigMajor, mknodBigMinor, p("toobig"),
			p("wide"), mknodWideMajor, mknodWideMinor)
	} else {
		src += fmt.Sprintf(`    // This process may not create a device node, so what the probe can
    // show is the REFUSAL: an Err, and no entry left behind.
    match (mknod(%[1]q, %[2]d, %[3]d, %[4]d)) { Ok(_) => { return 13; }, Err(_) => {} }
    match (lstat(%[1]q)) { Ok(_) => { return 14; }, Err(_) => {} }
`, p("chr"), mknodChr0600, mknodChrMajor, mknodChrMinor)
	}
	src += `    return 0;
}
`
	return src
}

// mknodCheckTree reads the tree back through Go's own Lstat — the kind
// bits and the raw st_rdev — so a `stat` that agreed with a broken
// `mknod` about the wrong number cannot make the probe self-consistent.
func mknodCheckTree(t *testing.T, dir string, withDev bool) {
	t.Helper()
	lstat := func(name string) *syscall.Stat_t {
		t.Helper()
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dir, name), &st); err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		return &st
	}
	if st := lstat("fifo"); st.Mode != mknodFifo0666 {
		t.Errorf("fifo mode = %o, want %o", st.Mode, mknodFifo0666)
	}
	if st := lstat("masked"); st.Mode != mknodFifo0666&^0o027 {
		t.Errorf("masked mode = %o, want %o — the umask did not filter the creation",
			st.Mode, mknodFifo0666&^0o027)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nodir")); !os.IsNotExist(err) {
		t.Errorf("nodir exists (lstat err = %v) — mknod created a parent", err)
	}
	if !withDev {
		if _, err := os.Lstat(filepath.Join(dir, "chr")); !os.IsNotExist(err) {
			t.Errorf("chr exists after a refused mknod (lstat err = %v)", err)
		}
		return
	}
	chr := lstat("chr")
	if chr.Mode != mknodChr0600 {
		t.Errorf("chr mode = %o, want %o", chr.Mode, mknodChr0600)
	}
	if want := linuxRdev(mknodChrMajor, mknodChrMinor); uint64(chr.Rdev) != want {
		t.Errorf("chr rdev = %#x, want %#x", chr.Rdev, want)
	}
	blk := lstat("blk")
	if blk.Mode != mknodBlk0600 {
		t.Errorf("blk mode = %o, want %o — the S_IFMT bits did not reach the kernel", blk.Mode, mknodBlk0600)
	}
	if want := linuxRdev(mknodChrMajor, mknodChrMinor); uint64(blk.Rdev) != want {
		t.Errorf("blk rdev = %#x, want %#x", blk.Rdev, want)
	}
	big := lstat("big")
	if want := linuxRdev(mknodBigMajor, mknodBigMinor); uint64(big.Rdev) != want {
		t.Errorf("big rdev = %#x, want %#x — the minor was not split around the major "+
			"(the legacy 8+8 layout would answer %#x)",
			big.Rdev, want, mknodBigMajor<<8|mknodBigMinor&0xff)
	}
	wide := lstat("wide")
	if want := linuxRdev(mknodWideMajor, mknodWideMinor); uint64(wide.Rdev) != want {
		t.Errorf("wide rdev = %#x, want %#x — the major's field is not twelve bits wide",
			wide.Rdev, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, "toobig")); !os.IsNotExist(err) {
		t.Errorf("toobig exists (lstat err = %v) — an out-of-range pair was accepted", err)
	}
}

// mknodDevAllowed measures whether THIS process may create a device node
// in `dir`, by creating one. A harness that assumed either way would
// either skip the device cases on a runner that could run them or fail on
// one that could not; this asks.
func mknodDevAllowed(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, ".devprobe")
	err := syscall.Mknod(probe, syscall.S_IFCHR|0o600, int(linuxRdev(1, 3)))
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

func TestX86_64Mknod(t *testing.T) {
	dir := t.TempDir()
	dev := mknodDevAllowed(t, dir)
	code, out := compileRunX86_64WithSetup(t, mknodSource(dir, dev), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see mknodSource)\n%s", code, out)
	}
	mknodCheckTree(t, dir, dev)
}

func TestArm64Mknod(t *testing.T) {
	dir := t.TempDir()
	dev := mknodDevAllowed(t, dir)
	out, code := compileAndRunArm64(t, mknodSource(dir, dev))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see mknodSource)\n%s", code, out)
	}
	mknodCheckTree(t, dir, dev)
}

// The arm64 SSA-direct backend packs the dev_t in its own hand-written
// sequence, in its own frame discipline, so it gets the probe rather than
// being taken on trust.
func TestArm64SSAMknod(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	dev := mknodDevAllowed(t, dir)
	bin := compileArm64SSA(t, fern, mknodSource(dir, dev), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see mknodSource)\n%s", code, stderr)
	}
	mknodCheckTree(t, dir, dev)
}

func TestInterpMknod(t *testing.T) {
	dir := t.TempDir()
	dev := mknodDevAllowed(t, dir)
	if code := runInterpExit(t, mknodSource(dir, dev)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see mknodSource)", code)
	}
	mknodCheckTree(t, dir, dev)
}

// Neither WASI preview can create a FIFO or a device node, and the
// nearest stand-in — a regular file where a FIFO was asked for — reads
// back as the wrong KIND rather than as a missing entry. So the answer on
// that target is a named refusal at check time, not a backend stub: no
// wasi profile grants `fsnode`, and E066 says so with the builtin's name
// and the call site's position.
func TestWASMMknodRefused(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	srcPath := filepath.Join(dir, "mk.fern")
	src := `function main(): i32 {
    match (mknod("p", 4096, 0, 0)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "mk.wasm"), srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err == nil {
		t.Fatalf("expected a refusal for mknod on wasm32-wasi, got success")
	}
	for _, want := range []string{"E066", "mknod", srcPath} {
		if !bytes.Contains(eb.Bytes(), []byte(want)) {
			t.Errorf("refusal missing %q:\n%s", want, eb.String())
		}
	}
}
