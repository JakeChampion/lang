// `chmod_at` end to end on every backend that provides it — the four
// natives, plus the Mach-O build where the host can run it. WASI has no
// permission bits, so E066 refuses the builtin there exactly as it refuses
// `chmod` (capability `fsmode`), and the last test in this file is that
// refusal rather than a wasm probe.
//
// What no unit test can assert: the FLAG picks the syscall on Linux —
// fchmodat for follow, fchmodat2 with AT_SYMLINK_NOFOLLOW otherwise — and
// each backend hand-writes that branch. A backend that issued the follow form
// for both would chmod a symlink's TARGET where the caller asked for the link
// and report Ok; one that issued fchmodat2 for both would work on a kernel
// that has it and ENOSYS on one that does not. So the probe chmods two
// symlinks to one file, one each way, in the order that makes a wrong branch
// leave a wrong mode behind, and the Go side reads everything back through
// its own Lstat.
//
// The two kernels answer the nofollow form differently, and the probe is
// parameterised on which one runs it. Linux keeps no mode on a symlink and
// says so — EOPNOTSUPP, or ENOSYS below fchmodat2 — and the builtin passes
// that through as the Err. Darwin keeps one and changes it.
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// The modes the probe writes. 0o5755 carries setuid and sticky, so a backend
// that masked to 0o777 before the syscall lands 0o755 instead.
const (
	chmodAtSuid    = 0o5755
	chmodAtPlain   = 0o640
	chmodAtNoFol   = 0o600
	chmodAtLink    = 0o444
	chmodAtThrough = 0o755
)

// chmodAtSource is the probe. Every failure returns its own exit code, so the
// number names the step. `linkHasMode` is whether the kernel running it keeps
// a mode on a symlink (Darwin) or refuses the nofollow form (Linux).
//
// The tree it operates on, all in `dir`:
//
//	plain     a regular file, the round-trip subject
//	tgt       a regular file, the target of both symlinks
//	flnk      a symlink to tgt, chmoded WITH following, first
//	lnk       a symlink to tgt, chmoded WITHOUT following, second
//	dangling  a symlink to nothing
func chmodAtSource(dir string, linkHasMode bool) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	// What the nofollow form on a symlink has to answer. On Darwin it is
	// Ok; on Linux an Err carrying the kernel's own errno text, and either
	// of the two is the kernel telling the truth.
	onLink := func(okCode, wrongCode int) string {
		if linkHasMode {
			return fmt.Sprintf(`Ok(_) => {}, Err(_) => { return %d; }`, okCode)
		}
		return fmt.Sprintf(`Ok(_) => { return %d; },
        Err(e) => { match (e) {
            Other(_, msg) => { if (msg != "Operation not supported" && msg != "Function not implemented") { return %d; } },
            _ => { return %d; }
        } }`, okCode, wrongCode, wrongCode+1)
	}
	return fmt.Sprintf(`function main(): i32 {
    // follow=true on a plain file is chmod: the low TWELVE bits verbatim,
    // setuid and sticky among them, and the umask not consulted.
    match (chmod_at(%[1]q, %[2]d, true)) { Ok(_) => {}, Err(_) => { return 1; } }
    match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[2]d as u32)) { return 2; } }, Err(_) => { return 3; } }
    match (chmod_at(%[1]q, %[3]d, true)) { Ok(_) => {}, Err(_) => { return 4; } }
    match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[3]d as u32)) { return 5; } }, Err(_) => { return 6; } }
    // follow=false on a plain file is the same change: there is no final
    // symlink for the flag to stop at. Under a kernel (or qemu) without
    // fchmodat2 it is ENOSYS instead, passed through rather than hidden;
    // the probe records that in a marker so the Go side expects the mode
    // this call did NOT change.
    match (chmod_at(%[1]q, %[4]d, false)) {
        Ok(_) => {
            match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[4]d as u32)) { return 8; } }, Err(_) => { return 9; } }
        },
        Err(e) => { match (e) {
            Other(_, msg) => {
                if (msg != "Function not implemented") { return 7; }
                match (stat(%[1]q)) { Ok(f) => { if ((f.mode & (4095 as u32)) != (%[3]d as u32)) { return 8; } }, Err(_) => { return 9; } }
                match (write_file(%[13]q, "")) { Ok(_) => {}, Err(_) => { return 7; } }
            },
            _ => { return 7; }
        } }
    }

    // A symlink chmoded WITH following reaches through to the target...
    match (chmod_at(%[5]q, %[6]d, true)) { Ok(_) => {}, Err(_) => { return 10; } }
    // ...and one chmoded WITHOUT following does not. This one comes second
    // so that a backend which followed anyway leaves its mode on tgt for
    // the Go side to find, rather than having it overwritten above.
    match (chmod_at(%[7]q, %[8]d, false)) { %[9]s }

    // A missing path names the kind rather than answering a silent Ok.
    match (chmod_at(%[10]q, %[8]d, true)) {
        Ok(_) => { return 20; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 21; } } }
    }
    // A dangling symlink followed cannot resolve; not followed, it is the
    // same answer as any other symlink.
    match (chmod_at(%[11]q, %[8]d, true)) {
        Ok(_) => { return 22; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 23; } } }
    }
    match (chmod_at(%[11]q, %[8]d, false)) { %[12]s }
    return 0;
}
`, p("plain"), chmodAtSuid, chmodAtPlain, chmodAtNoFol,
		p("flnk"), chmodAtThrough, p("lnk"), chmodAtLink, onLink(11, 12),
		p("nodir/gone"), p("dangling"), onLink(24, 25), p(chmodAtNoSysMarker))
}

// chmodAtNoSysMarker is the file the probe creates when the kernel running
// it has no fchmodat2 — qemu-user's aarch64 table, for one — so the nofollow
// form on a plain file was ENOSYS and left the mode alone.
const chmodAtNoSysMarker = "enosys"

// chmodAtLstatMode is the entry's own permission bits, through Go's Lstat.
func chmodAtLstatMode(t *testing.T, dir, name string) uint32 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(dir, name), &st); err != nil {
		t.Fatalf("lstat %s: %v", name, err)
	}
	return st.Mode & 0o7777
}

// chmodAtSeed builds the tree chmodAtSource operates on and returns the mode
// a fresh symlink has here — fixed 0o777 on Linux, the umask's leavings on
// Darwin — which is what an untouched link must still read back as.
func chmodAtSeed(t *testing.T, dir string) uint32 {
	t.Helper()
	for _, name := range []string{"plain", "tgt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	for _, l := range []struct{ target, name string }{
		{"tgt", "lnk"}, {"tgt", "flnk"}, {"no-such-target", "dangling"},
	} {
		if err := os.Symlink(filepath.Join(dir, l.target), filepath.Join(dir, l.name)); err != nil {
			t.Fatalf("seed symlink %s: %v", l.name, err)
		}
	}
	return chmodAtLstatMode(t, dir, "flnk")
}

// chmodAtCheckTree reads the tree back through Go's own Lstat, so a `stat`
// that agreed with a broken `chmod_at` about the wrong bits cannot make the
// probe self-consistent. `fresh` is what chmodAtSeed measured on a new link.
func chmodAtCheckTree(t *testing.T, dir string, fresh uint32, linkHasMode bool) {
	t.Helper()
	mode := func(name string) uint32 { return chmodAtLstatMode(t, dir, name) }
	// The last change to plain was the nofollow form, which lands where the
	// kernel has fchmodat2 and is an honest ENOSYS where it does not; the
	// probe's marker says which, and the mode it left has to agree.
	wantPlain := uint32(chmodAtNoFol)
	if _, err := os.Stat(filepath.Join(dir, chmodAtNoSysMarker)); err == nil {
		wantPlain = chmodAtPlain
		t.Logf("no fchmodat2 here (ENOSYS); the nofollow form on a plain file is checked as a refusal")
	}
	if got := mode("plain"); got != wantPlain {
		t.Errorf("plain mode = %04o, want %04o — the last chmod_at did not land, or an earlier one OR-ed instead of replacing", got, wantPlain)
	}
	if got := mode("tgt"); got != chmodAtThrough {
		t.Errorf("tgt mode = %04o, want %04o — either follow=true did not reach through flnk, or follow=false on lnk followed anyway", got, chmodAtThrough)
	}
	wantLink := fresh
	if linkHasMode {
		wantLink = chmodAtLink
	}
	if got := mode("lnk"); got != wantLink {
		t.Errorf("lnk mode = %04o, want %04o — follow=false did not treat the LINK as the kernel does here", got, wantLink)
	}
	if got := mode("dangling"); got != wantLink {
		t.Errorf("dangling mode = %04o, want %04o — follow=false did not treat the broken LINK as the kernel does here", got, wantLink)
	}
	if got := mode("flnk"); got != fresh {
		t.Errorf("flnk mode = %04o, want %04o — follow=true changed the LINK rather than its target", got, fresh)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nodir")); !os.IsNotExist(err) {
		t.Errorf("nodir exists (lstat err = %v) — chmod_at created a path", err)
	}
}

func TestX86_64ChmodAt(t *testing.T) {
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	code, out := compileRunX86_64WithSetup(t, chmodAtSource(dir, false), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)\n%s", code, out)
	}
	chmodAtCheckTree(t, dir, fresh, false)
}

func TestArm64ChmodAt(t *testing.T) {
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	out, code := compileAndRunArm64(t, chmodAtSource(dir, false))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)\n%s", code, out)
	}
	chmodAtCheckTree(t, dir, fresh, false)
}

// The Mach-O build issues XNU's fchmodat with the flag word itself, and XNU
// keeps a mode on a symlink — so on an Apple Silicon host the same probe
// runs natively with the other expectation.
func TestArm64DarwinChmodAt(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("arm64-darwin execution only runs on Apple Silicon")
	}
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	if code := runArm64Darwin(t, chmodAtSource(dir, true)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)", code)
	}
	chmodAtCheckTree(t, dir, fresh, true)
}

// The arm64 SSA-direct backend builds the same branch in its own hand-written
// sequence, in its own frame discipline, so it gets the probe rather than
// being taken on trust.
func TestArm64SSAChmodAt(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	bin := compileArm64SSA(t, fern, chmodAtSource(dir, false), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)\n%s", code, stderr)
	}
	chmodAtCheckTree(t, dir, fresh, false)
}

// The x86-64 SSA-direct backend builds the same branch in its own hand-written
// sequence, so it gets the probe too.
func TestX86_64SSAChmodAt(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	fern := buildFernCLI(t)
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	bin := compileX86_64SSA(t, fern, chmodAtSource(dir, false), os.Environ())
	code, stderr := runX86_64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)\n%s", code, stderr)
	}
	chmodAtCheckTree(t, dir, fresh, false)
}

// The interpreter runs on whichever kernel hosts the test, so its expectation
// follows the host: Linux refuses the nofollow form on a symlink, Darwin
// changes the link.
func TestInterpChmodAt(t *testing.T) {
	linkHasMode := runtime.GOOS == "darwin"
	dir := t.TempDir()
	fresh := chmodAtSeed(t, dir)
	if code := runInterpExit(t, chmodAtSource(dir, linkHasMode)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chmodAtSource)", code)
	}
	chmodAtCheckTree(t, dir, fresh, linkHasMode)
}

// Neither WASI preview has permission bits, so there is nothing for either
// follow mode to set. The answer on that target is the named refusal `chmod`
// already gets at check time: no wasi profile grants `fsmode`, and E066 says
// so with the builtin's name and the call site's position.
func TestWASMChmodAtRefused(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	srcPath := filepath.Join(dir, "cm.fern")
	src := `function main(): i32 {
    match (chmod_at("f", 420, false)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "cm.wasm"), srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err == nil {
		t.Fatalf("expected a refusal for chmod_at on wasm32-wasi, got success")
	}
	for _, want := range []string{"E066", "chmod_at", srcPath} {
		if !bytes.Contains(eb.Bytes(), []byte(want)) {
			t.Errorf("refusal missing %q:\n%s", want, eb.String())
		}
	}
}
