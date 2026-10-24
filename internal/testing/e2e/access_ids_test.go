package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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

func TestX86_64AccessAndEffectiveIds(t *testing.T) {
	readable, executable, missing := accessProbeTree(t)
	src := accessIdsSource(readable, executable, missing, os.Geteuid(), os.Getegid())
	code, out := compileRunX86_64WithSetup(t, src, nil)
	if code != 0 {
		t.Errorf("exit = %d, want 0 — the code names the case (see accessIdsSource)\n%s", code, out)
	}
}

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
