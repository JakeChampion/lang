package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// `chown_at` through the SELF-HOST IR path, on the two backends that provide
// it. Neither WASI preview records an owner on an entry, so the wasm leg is
// the capability refusal rather than a probe — `fsowner` is granted by no wasi
// profile.
//
// Nothing here can be proved by compiling. A `chown_at` that lowered to
// nothing, swapped its uid and gid, or passed the follow flag straight through
// where the kernel wants its inverse still links and still returns Ok. The
// last of those is the one a plain success cannot see: it chowns the symlink
// instead of its target, which is a change to a real entry. So the probe sets
// up two symlinks to the same file, chowns one each way, and the Go side reads
// all three back through its own Lstat.
//
// `internal/e2eselfhost` is PRIMARY for a self-host lowering change
// (docs/TEST-GATES.md): the fixpoint is self-referential and nothing in the
// compiler changes a file's owner.

// shChownIds is what the harness measured about this process: a group it may
// move a file it owns into, and whether it may change an owner at all.
type shChownIds struct {
	uid      int
	gid      int
	altGid   int
	mayChown bool
	altUid   int
}

// shMeasureChownIds establishes, by doing them, which of the two changes this
// process may make in `dir`, so neither arm of the probe is an assumption.
func shMeasureChownIds(t *testing.T, dir string) shChownIds {
	t.Helper()
	probe := filepath.Join(dir, ".chownprobe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("write ownership probe: %v", err)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(probe, &st); err != nil {
		t.Fatalf("lstat ownership probe: %v", err)
	}
	ids := shChownIds{uid: int(st.Uid), gid: int(st.Gid), altGid: -1, altUid: -1}

	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	// 0 and 1 exist on every unix and are what root reaches for; the chown
	// below is what decides, not this list.
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
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove ownership probe: %v", err)
	}
	return ids
}

// selfHostChownAtSeed builds the tree the probe operates on.
func selfHostChownAtSeed(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"plain", "tgt", "owned"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	for _, l := range []struct{ target, name string }{{"tgt", "lnk"}, {"tgt", "flnk"}} {
		if err := os.Symlink(filepath.Join(dir, l.target), filepath.Join(dir, l.name)); err != nil {
			t.Fatalf("seed symlink %s: %v", l.name, err)
		}
	}
}

// selfHostChownAtSource is the probe. Every failure returns its own exit code,
// so the number names the step.
func selfHostChownAtSource(dir string, ids shChownIds) string {
	p := func(name string) string { return filepath.Join(dir, name) }
	src := fmt.Sprintf(`function main(): i32 {
    // A group-only change: a uid of -1 must leave that half alone, which
    // is how chgrp is this call with the owner omitted.
    match (chown_at(%[1]q, -1, %[2]d, true)) { Ok(_) => {}, Err(_) => { return 1; } }
    // Both halves -1 changes nothing and is NOT an error.
    match (chown_at(%[1]q, -1, -1, true)) { Ok(_) => {}, Err(_) => { return 2; } }
    // A symlink chowned WITHOUT following: the link moves, its target
    // does not.
    match (chown_at(%[3]q, -1, %[2]d, false)) { Ok(_) => {}, Err(_) => { return 3; } }
    // ...and one chowned WITH following: the target moves, the link does
    // not. The pair pins the flag in both directions.
    match (chown_at(%[4]q, -1, %[2]d, true)) { Ok(_) => {}, Err(_) => { return 4; } }
    // A missing path names the kind rather than answering a silent Ok.
    match (chown_at(%[5]q, -1, %[2]d, true)) {
        Ok(_) => { return 5; },
        Err(e) => { match (e) { NotFound(_) => {}, _ => { return 6; } } }
    }
`, p("plain"), ids.altGid, p("lnk"), p("flnk"), p("nodir/gone"))
	if ids.mayChown {
		src += fmt.Sprintf(`    // An owner-only change: a gid of -1 leaves the group alone.
    match (chown_at(%[1]q, %[2]d, -1, true)) { Ok(_) => {}, Err(_) => { return 7; } }
`, p("owned"), ids.altUid)
	} else {
		src += fmt.Sprintf(`    // This process may not change an owner, so the probe shows the
    // refusal and that the entry was left as it was.
    match (chown_at(%[1]q, %[2]d, -1, true)) { Ok(_) => { return 7; }, Err(_) => {} }
`, p("owned"), 65534)
	}
	src += `    return 0;
}
`
	return src
}

// selfHostChownAtTree reads the tree back through Go's own Lstat rather than
// through the compiler's `stat`.
func selfHostChownAtTree(t *testing.T, dir string, ids shChownIds) {
	t.Helper()
	owner := func(name string) (int, int) {
		t.Helper()
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(dir, name), &st); err != nil {
			t.Fatalf("lstat %s: %v", name, err)
		}
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

// TestSelfHostChownAtIR is the x86-64 leg.
func TestSelfHostChownAtIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("chown_at test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	ids := shMeasureChownIds(t, work)
	selfHostChownAtSeed(t, work)
	cmd := exec.Command(driverBin, "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostChownAtSource(work, ids)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("__fern_chown_at")) {
		t.Fatal("chown_at did not reach the IR runtime path (no __fern_chown_at in the asm)")
	}
	progBin := buildBin(t, gcc, dir, "chown_at_prog", string(asm))
	run := exec.Command(progBin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("chown_at program exited %d, want 0 — the code names the step "+
			"(see selfHostChownAtSource)", code)
	}
	selfHostChownAtTree(t, work, ids)
}

// TestSelfHostChownAtIRArm64 is the same probe through the arm64 IR backend
// under qemu: a different syscall number, a different AT_SYMLINK_NOFOLLOW, and
// a second hand-written operand reversal.
func TestSelfHostChownAtIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("chown_at test runs only natively (mutates host paths)")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	work := t.TempDir()
	ids := shMeasureChownIds(t, work)
	selfHostChownAtSeed(t, work)
	cmd := exec.Command(driverBin, "-target", "arm64-linux", "-ir")
	cmd.Stdin = bytes.NewReader([]byte(selfHostChownAtSource(work, ids)))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	if !bytes.Contains(asm, []byte("bl __fn___fern_chown_at")) {
		t.Fatal("no `bl __fn___fern_chown_at` in the emitted asm — it did not lower " +
			"through the arm64 IR path")
	}
	bin := buildBinArm64(t, arm64gcc, dir, "chown_at_prog", string(asm))
	run := runArm64Bin(qemu, bin)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("chown_at arm64 program exited %d, want 0 — the code names the step "+
			"(see selfHostChownAtSource)", code)
	}
	selfHostChownAtTree(t, work, ids)
}
