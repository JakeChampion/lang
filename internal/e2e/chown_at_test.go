// `chown_at` end to end on every backend that provides it — the four
// natives. Neither WASI preview records an owner on a filesystem entry, so
// E066 refuses the builtin there (capability `fsowner`) and the last test in
// this file is that refusal rather than a wasm probe.
//
// What no unit test can assert: each backend builds the fchownat arguments by
// hand — three hand-written assembly sequences and one Go one — and three of
// the five are places to get something plausible-but-wrong. The -1 sentinel is
// a 32-bit 0xffffffff, so a backend that widened it to 64 bits before the call
// would hand the kernel a value it rejects; the FLAG is inverted relative to
// the argument (`follow` true means flags 0), so a backend that passed the
// bool straight through would chown the symlink where it was asked for the
// target; and the uid and gid are adjacent same-typed operands, so swapping
// them produces a call that succeeds against the wrong field. Every case here
// reads the result back through Go's own Lstat rather than through Fern's
// `stat`.
//
// PRIVILEGE. Changing an OWNER needs CAP_CHOWN; changing a GROUP needs only
// that the caller own the file and belong to the target group. So the harness
// MEASURES both rather than assuming either: it tries the calls itself in the
// same directory first, and the probe then asserts either the change or the
// same refusal. There is no arm that silently skips. This matters because CI
// and this dev machine disagree — as root every chown succeeds and the EPERM
// diagnostics would go unproven.
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

// chownIds is what the harness measured about this process: a group it may
// move a file it owns into, and whether it may change an owner at all.
type chownIds struct {
	uid      int
	gid      int
	altGid   int
	mayChown bool
	// altUid is meaningful only when mayChown; it is the owner the probe
	// moves a file to.
	altUid int
}

// measureChownIds establishes, by doing them, which of the two changes this
// process may make in `dir`. Nothing here is assumed: a harness that took
// either answer on trust would skip the privileged cases on a runner that
// could run them, or fail on one that could not.
func measureChownIds(t *testing.T, dir string) chownIds {
	t.Helper()
	probe := filepath.Join(dir, ".chownprobe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("write ownership probe: %v", err)
	}
	defer func() {
		if err := os.Remove(probe); err != nil {
			t.Fatalf("remove ownership probe: %v", err)
		}
	}()
	var st syscall.Stat_t
	if err := syscall.Lstat(probe, &st); err != nil {
		t.Fatalf("lstat ownership probe: %v", err)
	}
	ids := chownIds{uid: int(st.Uid), gid: int(st.Gid), altGid: -1, altUid: -1}

	// A group this process may move the file into, which is not the one it
	// is already in — otherwise a `chown_at` that did nothing would pass.
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	// Root may use any group, and on a runner whose only supplementary
	// group is its primary one there is nothing in `groups` to pick. 0 and
	// 1 exist on every unix, so they are the fallbacks to TRY — and a
	// failure below is what decides, not this list.
	for _, g := range append(groups, 0, 1) {
		if g == ids.gid {
			continue
		}
		if err := syscall.Chown(probe, -1, g); err == nil {
			ids.altGid = g
			break
		}
	}
	if ids.altGid < 0 {
		t.Fatalf("no group this process may move %s into, and it belongs to %v — "+
			"the probe cannot prove a group change anywhere", probe, groups)
	}
	// Put it back, so what the probe observes later is the original.
	if err := syscall.Chown(probe, -1, ids.gid); err != nil {
		t.Fatalf("restoring the probe's group: %v", err)
	}

	// And whether an OWNER may change. 65534 (nobody) is the conventional
	// unprivileged id and exists on every runner this suite meets.
	const nobody = 65534
	target := nobody
	if ids.uid == nobody {
		target = 0
	}
	switch err := syscall.Chown(probe, target, -1); {
	case err == nil:
		ids.mayChown = true
		ids.altUid = target
	case err == syscall.EPERM || err == syscall.EACCES:
		t.Logf("owners are not changeable here (%v); the probe asserts the refusal instead", err)
	default:
		t.Fatalf("owner probe failed for a reason that is not a privilege one: %v", err)
	}
	return ids
}

// chownAtSource is the probe. Every failure returns its own exit code, so the
// number names the step.
//
// The tree it builds, all in `dir`:
//
//	plain   a regular file, the group-change and no-op subject
//	tgt     a regular file, the target of both symlinks
//	lnk     a symlink to tgt, chowned WITHOUT following
//	flnk    a symlink to tgt, chowned WITH following
//	owned   a regular file, the owner-change subject
func chownAtSource(dir string, ids chownIds) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	src := fmt.Sprintf(`function main(): i32 {
    // A group-only change: the uid is -1, which must leave that half
    // alone. The Go side asserts the uid did not move.
    match (chown_at(%[1]q, -1, %[2]d, true)) { Ok(_) => {}, Err(_) => { return 1; } }
    // Both halves -1 is a no-op and is NOT an error — that is fchownat's
    // own answer, and chown --from relies on it.
    match (chown_at(%[1]q, -1, -1, true)) { Ok(_) => {}, Err(_) => { return 2; } }

    // A symlink chowned WITHOUT following: the LINK moves and its target
    // does not. A backend that passed the follow flag through instead of
    // inverting it into AT_SYMLINK_NOFOLLOW would move the target here
    // and report exactly the same Ok.
    match (chown_at(%[3]q, -1, %[2]d, false)) { Ok(_) => {}, Err(_) => { return 3; } }
    // ...and one chowned WITH following: the target moves and the link
    // does not. The two together pin the flag in both directions.
    match (chown_at(%[4]q, -1, %[2]d, true)) { Ok(_) => {}, Err(_) => { return 4; } }

    // A missing path names the kind rather than answering a silent Ok.
    match (chown_at(%[5]q, -1, %[2]d, true)) {
        Ok(_) => { return 5; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } }
    }
    // A dangling symlink, not followed: the LINK exists, so this
    // succeeds where the followed form below cannot resolve a target.
    match (chown_at(%[6]q, -1, %[2]d, false)) { Ok(_) => {}, Err(_) => { return 7; } }
    match (chown_at(%[6]q, -1, %[2]d, true)) {
        Ok(_) => { return 8; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 9; } } }
    }
`, p("plain"), ids.altGid, p("lnk"), p("flnk"), p("nodir/gone"), p("dangling"))

	if ids.mayChown {
		src += fmt.Sprintf(`    // An owner-only change: the gid is -1 and must be left alone.
    match (chown_at(%[1]q, %[2]d, -1, true)) { Ok(_) => {}, Err(_) => { return 10; } }
`, p("owned"), ids.altUid)
	} else {
		src += fmt.Sprintf(`    // This process may not change an owner, so what the probe can show
    // is the refusal — and that the entry was left as it was.
    match (chown_at(%[1]q, %[2]d, -1, true)) { Ok(_) => { return 10; }, Err(_) => {} }
`, p("owned"), 65534)
	}
	src += `    return 0;
}
`
	return src
}

// chownAtSeed builds the tree chownAtSource operates on.
func chownAtSeed(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"plain", "tgt", "owned"} {
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
}

// chownAtCheckTree reads the tree back through Go's own Lstat, so a `stat`
// that agreed with a broken `chown_at` about the wrong ids cannot make the
// probe self-consistent.
func chownAtCheckTree(t *testing.T, dir string, ids chownIds) {
	t.Helper()
	lstat := func(name string) *syscall.Stat_t {
		t.Helper()
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dir, name), &st); err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
		return &st
	}
	owner := func(name string) (int, int) {
		st := lstat(name)
		return int(st.Uid), int(st.Gid)
	}

	if uid, gid := owner("plain"); gid != ids.altGid {
		t.Errorf("plain gid = %d, want %d — the group did not change", gid, ids.altGid)
	} else if uid != ids.uid {
		t.Errorf("plain uid = %d, want %d — a uid of -1 did not leave the owner alone", uid, ids.uid)
	}

	// The unfollowed symlink moved and its target did not; the followed one
	// is the other way round. `tgt` is shared, so it carries the followed
	// case's change and neither link's.
	if _, gid := owner("lnk"); gid != ids.altGid {
		t.Errorf("lnk gid = %d, want %d — follow=false did not change the LINK", gid, ids.altGid)
	}
	if _, gid := owner("flnk"); gid != ids.gid {
		t.Errorf("flnk gid = %d, want %d — follow=true changed the LINK rather than its target",
			gid, ids.gid)
	}
	if _, gid := owner("tgt"); gid != ids.altGid {
		t.Errorf("tgt gid = %d, want %d — follow=true did not reach through the symlink",
			gid, ids.altGid)
	}
	if _, gid := owner("dangling"); gid != ids.altGid {
		t.Errorf("dangling gid = %d, want %d — follow=false did not change a broken link",
			gid, ids.altGid)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nodir")); !os.IsNotExist(err) {
		t.Errorf("nodir exists (lstat err = %v) — chown_at created a path", err)
	}

	uid, gid := owner("owned")
	if !ids.mayChown {
		if uid != ids.uid {
			t.Errorf("owned uid = %d, want %d — a refused chown changed the owner anyway", uid, ids.uid)
		}
		return
	}
	if uid != ids.altUid {
		t.Errorf("owned uid = %d, want %d — the owner did not change", uid, ids.altUid)
	}
	if gid != ids.gid {
		t.Errorf("owned gid = %d, want %d — a gid of -1 did not leave the group alone", gid, ids.gid)
	}
}

func TestX86_64ChownAt(t *testing.T) {
	dir := t.TempDir()
	ids := measureChownIds(t, dir)
	chownAtSeed(t, dir)
	code, out := compileRunX86_64WithSetup(t, chownAtSource(dir, ids), nil)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chownAtSource)\n%s", code, out)
	}
	chownAtCheckTree(t, dir, ids)
}

func TestArm64ChownAt(t *testing.T) {
	dir := t.TempDir()
	ids := measureChownIds(t, dir)
	chownAtSeed(t, dir)
	out, code := compileAndRunArm64(t, chownAtSource(dir, ids))
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chownAtSource)\n%s", code, out)
	}
	chownAtCheckTree(t, dir, ids)
}

// The arm64 SSA-direct backend builds the same call in its own hand-written
// sequence, in its own frame discipline, so it gets the probe rather than
// being taken on trust.
func TestArm64SSAChownAt(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	ids := measureChownIds(t, dir)
	chownAtSeed(t, dir)
	bin := compileArm64SSA(t, fern, chownAtSource(dir, ids), os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, dir, os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chownAtSource)\n%s", code, stderr)
	}
	chownAtCheckTree(t, dir, ids)
}

func TestInterpChownAt(t *testing.T) {
	dir := t.TempDir()
	ids := measureChownIds(t, dir)
	chownAtSeed(t, dir)
	if code := runInterpExit(t, chownAtSource(dir, ids)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chownAtSource)", code)
	}
	chownAtCheckTree(t, dir, ids)
}

// Neither WASI preview records an owner on an entry — preview 1's `filestat`
// has no uid or gid field and the component model's `descriptor-stat` none
// either — so there is nothing to set and no owner a success would describe.
// The answer on that target is a named refusal at check time, not a backend
// stub: no wasi profile grants `fsowner`, and E066 says so with the builtin's
// name and the call site's position.
func TestWASMChownAtRefused(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fern")
	build := exec.Command("go", "build", "-o", bin, "github.com/jakechampion/lang/cmd/fern")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build fern: %v\n%s", err, out)
	}
	srcPath := filepath.Join(dir, "ch.fern")
	src := `function main(): i32 {
    match (chown_at("f", 0, 0, true)) { Ok(_) => { return 0; }, Err(_) => { return 1; } }
}
`
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	emit := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, "ch.wasm"), srcPath)
	var eb bytes.Buffer
	emit.Stderr = &eb
	if err := emit.Run(); err == nil {
		t.Fatalf("expected a refusal for chown_at on wasm32-wasi, got success")
	}
	for _, want := range []string{"E066", "chown_at", srcPath} {
		if !bytes.Contains(eb.Bytes(), []byte(want)) {
			t.Errorf("refusal missing %q:\n%s", want, eb.String())
		}
	}
}
