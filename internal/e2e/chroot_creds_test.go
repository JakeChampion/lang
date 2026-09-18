package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// chroot(path), setgroups(gids), setgid(gid) and setuid(uid) — the four
// primitives chroot(1) needs beyond what the tree already had (#9678).
//
// PRIVILEGE DECIDES WHICH HALF OF THIS RUNS, so the source branches on
// geteuid() rather than assuming either. The GitHub runners are uid 1001 and
// reach only the refusals; `scripts/devbox` and the dev container are root and
// reach the syscalls for real. Asserting one and skipping the other would
// leave whichever half CI does not run ungated forever.
//
// The range refusals are the part that is the same everywhere, because they
// never reach the kernel at all: an id outside 32 unsigned bits is EINVAL in
// the helper. That is not defensive tidiness — the kernel reads the low 32
// bits of the register for a uid_t argument, so setuid(2^32 + 1) would
// otherwise set uid 1, and GNU's "no id" spelling (uid_t) -1 has every high
// bit set as an i64 and would set uid 0xFFFFFFFF.
const chrootCredsSource = `
// Naming every variant forces each builtin's Err payload to BE the IoError
// enum rather than a struct of that name — the shape chdir shipped with
// (#9090), invisible to a test that never looks inside the error.
function code_of(e: IoError): i32 {
    match (e) {
        NotFound(_) => { return 1; },
        PermissionDenied(_) => { return 2; },
        AlreadyExists(_) => { return 3; },
        InvalidUtf8(_) => { return 4; },
        Interrupted => { return 5; },
        Unsupported => { return 6; },
        Other(c, m) => { return 7; }
    }
}

function main(): i32 {
    var uid0: u32 = geteuid();
    var root: boolean = uid0 == 0;
    var neg: i64 = 0i64 - 1i64;
    var big: i64 = 4294967296i64;

    // A path that does not exist is an Err, and WHICH errno is the kernel's
    // choice rather than ours: Linux looks the path up BEFORE checking
    // CAP_SYS_CHROOT, so it answers ENOENT at any privilege, while XNU's
    // suser() check runs before namei and answers EPERM to an unprivileged
    // caller. Both are truthful, so what is asserted is that it failed and
    // named one of the two — ENOENT as NotFound, EPERM as Other, since
    // PermissionDenied is EACCES's variant and EPERM has none of its own.
    match (chroot("/no-such-directory-at-all-9678")) {
        Ok(v) => { return 61; },
        Err(e) => {
            var c: i32 = code_of(e);
            if (c != 1) { if (c != 7) { return 62; } }
        }
    }

    // The refusals. Each returns Other, since EINVAL has no named variant.
    match (setuid(neg)) {
        Ok(v) => { return 63; },
        Err(e) => { if (code_of(e) != 7) { return 64; } }
    }
    match (setgid(neg)) {
        Ok(v) => { return 65; },
        Err(e) => { if (code_of(e) != 7) { return 66; } }
    }
    match (setuid(big)) {
        Ok(v) => { return 67; },
        Err(e) => { if (code_of(e) != 7) { return 68; } }
    }
    match (setgid(big)) {
        Ok(v) => { return 69; },
        Err(e) => { if (code_of(e) != 7) { return 70; } }
    }
    // One bad element refuses the WHOLE list rather than the good prefix of
    // it: a partial set would be a credential nobody asked for.
    match (setgroups([big, 0i64])) {
        Ok(v) => { return 71; },
        Err(e) => { if (code_of(e) != 7) { return 72; } }
    }

    // A refused id did not take effect. Checked against the value read at
    // entry rather than against 0, because this runs at both privileges.
    if (geteuid() != uid0) { return 73; }

    if (root) {
        // Root can move the root, and the move is proved by resolving a path
        // that only exists INSIDE the new one: /dev/null is /null once /dev is
        // the root. Asking the kernel to resolve it beats asking a second
        // builtin to agree that the move happened.
        match (stat("/null")) {
            Ok(v) => { return 74; },
            Err(e) => {}
        }
        match (chroot("/dev")) {
            Ok(v) => {},
            Err(e) => { return 75; }
        }
        match (stat("/null")) {
            Ok(v) => {},
            Err(e) => { return 76; }
        }
        // And the supplementary set can be emptied. An empty list is a real
        // request — "in no supplementary groups" — and the one
        // chroot --groups="" makes, so it reaches the kernel rather than
        // answering Ok without asking.
        match (setgroups([])) {
            Ok(v) => {},
            Err(e) => { return 77; }
        }
        if (getgroups().len() != 0) { return 78; }
    } else {
        // Unprivileged, every one of the three is EPERM — which is an answer,
        // not a no-op that claims a change nothing made.
        //
        // EPERM arrives as Other carrying the message, NOT as
        // PermissionDenied: __fern_io_error names ENOENT, EACCES, EEXIST,
        // EINTR and EILSEQ and leaves every other errno to Other, and that
        // is load-bearing rather than incidental. The utilities print the
        // message out of Other, so chroot's "cannot change root directory to
        // 'x': Operation not permitted" is byte-for-byte GNU's only because
        // EPERM is not folded into a variant that drops it. Checking the
        // message is what makes this stronger than "it failed somehow".
        match (setgroups([])) {
            Ok(v) => { return 79; },
            Err(e) => {
                match (e) {
                    Other(p, m) => { if (m != "Operation not permitted") { return 80; } },
                    _ => { return 80; }
                }
            }
        }
        match (setgid(0i64)) {
            Ok(v) => { return 81; },
            Err(e) => {
                match (e) {
                    Other(p, m) => { if (m != "Operation not permitted") { return 82; } },
                    _ => { return 82; }
                }
            }
        }
        match (setuid(0i64)) {
            Ok(v) => { return 83; },
            Err(e) => {
                match (e) {
                    Other(p, m) => { if (m != "Operation not permitted") { return 84; } },
                    _ => { return 84; }
                }
            }
        }
    }
    return 0;
}`

func runChrootCredsChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	if got := run(t, chrootCredsSource); got != 0 {
		t.Fatalf("exit %d, want 0 — the code names the step (see the source above)", got)
	}
}

func TestX86_64ChrootCreds(t *testing.T) {
	runChrootCredsChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunX86_64(t, src)
		return exit
	})
}

func TestArm64ChrootCreds(t *testing.T) {
	runChrootCredsChecks(t, func(t *testing.T, src string) int {
		_, exit := compileAndRunArm64(t, src)
		return exit
	})
}

func TestArm64SSAChrootCreds(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	runChrootCredsChecks(t, func(t *testing.T, src string) int {
		bin := compileArm64SSA(t, fern, src, os.Environ())
		code, _ := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
		return code
	})
}

// The x86-64 SSA leg, against the flat x86-64 emitter rather than against a
// hardcoded expectation: the two backends are two implementations of the same
// four builtins, so the only question is whether they agree. #9559's stack
// gave this backend `chdir` and the credential getters, which is what made
// their write side reachable here at all.
func TestX86_64SSAChrootCreds(t *testing.T) {
	qemu := x86QemuOrEmpty(t)
	bin := buildFernCLI(t)
	dir := t.TempDir()

	flat := runPathProbe(t, bin, qemu, dir, "creds", "flat", chrootCredsSource, "")
	if flat != 0 {
		t.Fatalf("the flat emitter itself reports %d — the probe is wrong, not the SSA backend", flat)
	}
	if ssa := runPathProbe(t, bin, qemu, dir, "creds", "ssa", chrootCredsSource, ""); ssa != flat {
		t.Errorf("-backend ssa reports %d where the flat emitter reports %d.\n\n"+
			"Each code names one step in the probe source above.", ssa, flat)
	}
}

// The Mach-O leg, run natively on Apple Silicon. XNU's BSD numbers for these
// four are 61 / 23 / 181 / 80 and share no arithmetic with Linux's 51 / 146 /
// 144 / 159 — 4.3BSD put getgroups and setgroups adjacent at 79 and 80 while
// setuid and setgid came from different eras of the ABI — so a table filled in
// by offsetting the Linux row would issue four different syscalls here. This
// is what notices.
func TestArm64DarwinChrootCreds(t *testing.T) {
	bin := buildFernCLI(t)
	runChrootCredsChecks(t, func(t *testing.T, src string) int {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "prog.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		out := filepath.Join(dir, "prog")
		if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, srcPath).CombinedOutput(); err != nil {
			t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
		}
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			t.Skip("execution check only runs on Apple Silicon")
		}
		cmd := exec.Command(out)
		cmd.Dir = dir
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	})
}
