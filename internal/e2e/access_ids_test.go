package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// accessProbeTree writes three paths with different permission bits, so the
// probe below can ask a question whose answer is not the same for all of them.
func accessProbeTree(t *testing.T) (readable, executable, missing string) {
	t.Helper()
	dir := t.TempDir()
	readable = filepath.Join(dir, "readable.txt")
	if err := os.WriteFile(readable, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(readable, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	executable = filepath.Join(dir, "runnable.sh")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	missing = filepath.Join(dir, "not-here.txt")
	return readable, executable, missing
}

// accessIdsSource exercises `access` on all four POSIX modes and both
// effective-id builtins. Each failure returns its own code.
//
// The X_OK cases are the ones that carry signal whatever uid the suite runs
// as: the execute bit is checked even for root (which is why `test -x` on a
// 0644 file is false in a root shell), while R_OK and W_OK are granted to root
// unconditionally. So a 0644 file is NOT executable and a 0755 one is, on
// every account.
func accessIdsSource(readable, executable, missing string, euid, egid int) string {
	return fmt.Sprintf(`function ok(p: string, m: i32): boolean {
    match (access(p, m)) {
        Ok(_) => { return true; },
        Err(e) => { return false; },
    }
    return false;
}
function notfound(p: string, m: i32): boolean {
    match (access(p, m)) {
        Ok(_) => { return false; },
        Err(e) => {
            match (e) {
                NotFound(_) => { return true; },
                _ => { return false; },
            }
        },
    }
    return false;
}
function main(): i32 {
    // F_OK = 0: does the path exist?
    if (!ok(%[1]q, 0)) { return 1; }
    if (ok(%[3]q, 0)) { return 2; }
    // The errno reaches the caller, so a missing path is distinguishable
    // from a refusal — which is the whole reason this returns a Result.
    if (!notfound(%[3]q, 0)) { return 3; }
    // R_OK = 4 on a 0644 file.
    if (!ok(%[1]q, 4)) { return 4; }
    // X_OK = 1: false for 0644, true for 0755, for every uid including 0.
    if (ok(%[1]q, 1)) { return 5; }
    if (!ok(%[2]q, 1)) { return 6; }
    // R_OK|W_OK|X_OK on the 0755 file.
    if (!ok(%[2]q, 7)) { return 7; }
    // The effective ids the answers above were computed against.
    if (geteuid() != (%[4]d as u32)) { return 8; }
    if (getegid() != (%[5]d as u32)) { return 9; }
    return 0;
}
`, readable, executable, missing, euid, egid)
}

// `access` has to ask about the EFFECTIVE ids — GNU's `test -r` / `-w` / `-x`
// is specified against `euidaccess`, not `access(2)`, which asks about the
// REAL ones and answers a different question for a set-uid process. Every
// native backend therefore reaches for the flag-taking call (faccessat2 on
// Linux, faccessat on Darwin) with AT_EACCESS set, and the flag's VALUE
// differs between the two (0x200 vs 0x10).
//
// What this test can prove without a set-uid binary is that the call is issued
// with a flags word the kernel accepts at all: without AT_EACCESS support the
// syscall answers EINVAL or ENOSYS and every case below fails at once.
func TestX86_64AccessAndEffectiveIds(t *testing.T) {
	readable, executable, missing := accessProbeTree(t)
	src := accessIdsSource(readable, executable, missing, os.Geteuid(), os.Getegid())
	code, out := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see accessIdsSource)\n%s", code, out)
	}
}

// The interpreter routes `access` through syscall.Faccessat with AT_EACCESS on
// Linux and, where the platform has no Faccessat, through glibc's own
// euidaccess fallback — the mode-bit evaluation the kernel would do. Both
// answer this probe.
func TestInterpAccessAndEffectiveIds(t *testing.T) {
	readable, executable, missing := accessProbeTree(t)
	src := accessIdsSource(readable, executable, missing, os.Geteuid(), os.Getegid())
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see accessIdsSource)", code)
	}
}

func TestArm64AccessAndEffectiveIds(t *testing.T) {
	readable, executable, missing := accessProbeTree(t)
	src := accessIdsSource(readable, executable, missing, os.Geteuid(), os.Getegid())
	out, code := compileAndRunArm64(t, src)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see accessIdsSource)\n%s", code, out)
	}
}
