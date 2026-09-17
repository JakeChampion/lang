package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// The x86-64 leg runs the emitted binary the same way the arm64 leg does:
	// directly on a matching host, under the emulator otherwise. This test
	// lands in the catch-all `test-e2e-other` lane, whose matrix includes an
	// aarch64 runner, so exec'ing an x86-64 ELF there fails to START — and a
	// command that never started leaves ProcessState nil, which made the
	// ExitCode() below a panic rather than a failure.
	var run *exec.Cmd
	if target == "arm64-linux" {
		run = runArm64Bin(qemu, out)
	} else if len(x86Runner) == 0 {
		run = exec.Command(out)
	} else {
		run = exec.Command(x86Runner[0], append(append([]string{}, x86Runner[1:]...), out)...)
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
	// Not X86_64Tooling: that skips the whole test when it cannot run an
	// x86-64 binary, and the arm64 half still has something to say on an
	// aarch64 host. The lookup half lets each target's legs decide alone.
	_, x86Runner, x86OK := e2eharness.LookupX86_64Tooling()
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
