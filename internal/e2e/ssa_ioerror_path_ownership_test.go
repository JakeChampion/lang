package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

// A path builtin that FAILS must not destroy the string it was given.
//
// The IoError it returns names the path, and the box that carries it is
// dropped when the caller's `Err` arm ends — so the box needs a reference of
// its own. Both SSA backends used to store the caller's pointer without
// retaining it, so the drop freed a buffer the caller still held: #9543, where
// `lstat(p)` left `p.len()` reading 0 and the next allocation was handed p's
// block. The stack-machine emitters were never affected.
//
// The check is `p.len()` after the call, returned as the exit status, which is
// the cheapest thing that distinguishes "still mine" from "recycled". Every
// case uses a path that cannot exist, so the failure edge is the one taken.
const ioErrPathOwnershipSrc = `function mk(a: string): string { return a + ""; }
function main(): i32 {
    var p: string = mk("/fern/no/such/path/here");
    match (BUILTIN(p)) { Ok(_) => {}, Err(_) => {} }
    return p.len();
}
`

// wantIoErrPathLen is the length of the literal above, which is what p.len()
// must still report once the failing call has returned.
const wantIoErrPathLen = len("/fern/no/such/path/here")

// x86_64RunnerOrEmpty is arm64QemuOrEmpty's x86-64 twin: an empty prefix on an
// amd64 Linux host, meaning run the binary directly; a qemu-x86_64 prefix
// anywhere else; and ok=false only when there is no way to run such a binary
// at all.
//
// Deliberately not LookupX86_64Tooling, which reports ok=false without an
// x86-64 gcc on PATH. This test never links with one — `fern -target
// x86-64-linux -o out` uses the CLI's own in-process linker — so gating on a
// compiler it does not use would skip these legs on a gcc-less amd64 host,
// where they run perfectly well.
func x86_64RunnerOrEmpty() (runner []string, ok bool) {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return nil, true
	}
	if p, err := exec.LookPath("qemu-x86_64"); err == nil {
		return []string{p}, true
	}
	return nil, false
}

func ioErrPathLen(t *testing.T, bin, target, backend, qemu string, x86Runner []string, dir, builtin string) int {
	t.Helper()
	name := builtin + "_" + backend
	src := filepath.Join(dir, name+".fern")
	out := filepath.Join(dir, name+".bin")
	body := []byte(strings.ReplaceAll(ioErrPathOwnershipSrc, "BUILTIN", builtin))
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", src, err)
	}
	args := []string{"-target", target}
	if backend != "" {
		args = append(args, "-backend", backend)
	}
	args = append(args, "-o", out, src)
	compile := exec.Command(bin, args...)
	compile.Env = e2eharness.ChildEnv()
	if o, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile %s for %s (-backend %q) failed: %v\n%s", builtin, target, backend, err, o)
	}
	// This test lands in the catch-all `test-e2e-other` lane, whose matrix
	// includes an aarch64 runner, so the x86-64 leg has to dispatch the way
	// the arm64 leg already does. RunX86_64Bin is that dispatch, and its doc
	// comment names the exact failure a bare exec produces there: binfmt_misc
	// makes the exec appear to work, and the program then SIGSEGVs where the
	// explicit qemu-x86_64 prefix runs it correctly.
	var run *exec.Cmd
	if target == "arm64-linux" {
		run = runArm64Bin(qemu, out)
	} else {
		run = e2eharness.RunX86_64Bin(x86Runner, out)
	}
	run.Env = e2eharness.ChildEnv()
	err := run.Run()
	// Every case here exits NON-ZERO on purpose — the exit status is p.len() —
	// so a non-nil error is the normal path and must not fail the test. What
	// distinguishes "ran and exited 20" from "never started" is ProcessState.
	if run.ProcessState == nil {
		t.Fatalf("run %s for %s (-backend %q) never started: %v", builtin, target, backend, err)
	}
	return run.ProcessState.ExitCode()
}

func TestSSABackendsKeepThePathAFailingBuiltinWasGiven(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	x86Runner, x86OK := x86_64RunnerOrEmpty()
	dir := t.TempDir()

	// Per target, because the two SSA backends emit different subsets: a
	// builtin with no emitter refuses to compile rather than miscompiling, and
	// naming it here would fail the build instead of testing anything.
	//
	// `read_file` is the one case that did NOT exhibit #9543 on either backend
	// — verified by mutation, where every other case below flips and it does
	// not. It stays as a guard on a builtin with the same shape, not as
	// evidence for the fix.
	for _, tc := range []struct {
		target   string
		builtins []string
	}{
		{"arm64-linux", []string{"lstat", "stat", "read_file", "remove_file", "read_link"}},
		{"x86-64-linux", []string{"stat", "read_file", "remove_file"}},
	} {
		for _, b := range tc.builtins {
			t.Run(tc.target+"/"+b, func(t *testing.T) {
				if tc.target == "x86-64-linux" && !x86OK {
					t.Skip("no way to run an x86-64 binary here: non-amd64 host and no qemu-x86_64 on PATH")
				}
				flat := ioErrPathLen(t, bin, tc.target, "flat", qemu, x86Runner, dir, b)
				ssa := ioErrPathLen(t, bin, tc.target, "ssa", qemu, x86Runner, dir, b)
				if flat != wantIoErrPathLen {
					t.Fatalf("the stack-machine emitter reports p.len() = %d after a failing %s, want %d — "+
						"the fixture is wrong, not the backend", flat, b, wantIoErrPathLen)
				}
				if ssa != flat {
					t.Errorf("after a failing %s, -backend ssa reports p.len() = %d and the stack machine %d.\n\n"+
						"The IoError box carries the path and its drop releases it, so the box must retain "+
						"what it is given — see emitIoErrorOwningPath (arm64ssa) and ssaRetainPathForIoErr "+
						"(x86_64ssa). A borrowed pointer there frees the caller's string (#9543).", b, ssa, flat)
				}
			})
		}
	}
	if testing.Verbose() {
		fmt.Printf("io-error path ownership: every case reports %d\n", wantIoErrPathLen)
	}
}
