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

type chownIds struct {
	uid      int
	gid      int
	altGid   int
	mayChown bool
	altUid   int
}

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

	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
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
	if err := syscall.Chown(probe, -1, ids.gid); err != nil {
		t.Fatalf("restoring the probe's group: %v", err)
	}

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

func TestInterpChownAt(t *testing.T) {
	dir := t.TempDir()
	ids := measureChownIds(t, dir)
	chownAtSeed(t, dir)
	if code := runInterpExit(t, chownAtSource(dir, ids)); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see chownAtSource)", code)
	}
	chownAtCheckTree(t, dir, ids)
}

func TestWASMChownAtRefused(t *testing.T) {
	dir := t.TempDir()
	bin := buildFernCLI(t)
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
