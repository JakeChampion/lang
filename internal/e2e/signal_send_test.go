// `signal_send` (#8377) end to end on every backend that provides it.
//
// Two programs. The first asks for outcomes the runner is guaranteed to get
// the same answer to whoever it runs as: the caller may always signal its own
// process group, a pid above the kernel's ceiling is always ESRCH, and an
// impossible signal number is always EINVAL. None of them needs the test to
// know whether it is root, and none delivers anything.
//
// The second forks and actually kills the child, because a Result that
// classifies errnos correctly still proves nothing about delivery. It has no
// interpreter leg: the interpreter cannot bare-fork (Go's runtime is
// threaded) and answers -38 there.
//
// wasm has no leg at all: neither WASI preview has a process table to name a
// target in, so the `proc` capability withholds the builtin and E066 refuses
// the program at check time. TestWASMSignalSendRefused is that half.
package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// Each failing step returns its own code.
//
// 4194304 is 2^22, one above the largest pid_max Linux accepts, so no process
// can ever hold it — where a pid merely "not in use right now" could be
// reassigned between the compile and the run.
const signalSendSource = `function main(): i32 {
    // pid 0 is the caller's OWN process group, which it may always signal,
    // and signal 0 delivers nothing. The Ok proves the pid reaches the kernel
    // unfiltered: process_alive answers false for this same argument, because
    // a liveness question is about one process and this is not one.
    match (signal_send(0, 0)) {
        Ok(_) => {},
        Err(_) => { return 1; }
    }

    // Above the kernel's own pid ceiling: ESRCH. None of the three errnos
    // kill(2) returns has a named IoError variant, so each arrives as
    // Other(path, strerror) — and the path is empty, because the primitive
    // never saw the text a caller parsed the pid out of.
    match (signal_send(4194304, 0)) {
        Ok(_) => { return 2; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
                    if (p.len() != 0) { return 3; }
                    if (msg != "No such process") { return 4; }
                },
                _ => { return 5; }
            }
        }
    }

    // An impossible signal number against a target that DOES exist: EINVAL.
    // The target has to exist for this to be portable - Linux looks the pid
    // up first and answers ESRCH for a bad signal to a missing process, where
    // Darwin validates the signal first and answers EINVAL (measured, both).
    match (signal_send(0, 12345)) {
        Ok(_) => { return 6; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
                    if (p.len() != 0) { return 7; }
                    if (msg != "Invalid argument") { return 8; }
                },
                _ => { return 9; }
            }
        }
    }
    return 0;
}
`

// The delivery proof. The child sleeps far longer than the parent needs, so
// the only way it reaches the parent's waitpid is the signal; a child that
// exited on its own would report 0 rather than 128+9.
const signalSendDeliverySource = `function main(): i32 {
    var pid: i32 = proc_fork();
    if (pid < 0) { return 1; }
    if (pid == 0) {
        sleep_ms(60000 as i64);
        // Only reachable if the signal never arrived. A distinct code so a
        // child that outlives the sleep is not read as a clean exit.
        exit(70);
    }
    match (signal_send(pid, 9)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    // proc_waitpid reports a signal death as 128+signal.
    var st: i32 = proc_waitpid(pid);
    if (st != 137) { return 3; }
    return 0;
}
`

func TestX86_64SignalSend(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, signalSendSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendSource)\n%s", code, out)
	}
}

func TestX86_64SignalSendDelivers(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, signalSendDeliverySource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendDeliverySource)\n%s", code, out)
	}
}

func TestArm64SignalSend(t *testing.T) {
	out, code := compileAndRunArm64(t, signalSendSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendSource)\n%s", code, out)
	}
}

func TestArm64SignalSendDelivers(t *testing.T) {
	out, code := compileAndRunArm64(t, signalSendDeliverySource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendDeliverySource)\n%s", code, out)
	}
}

func TestArm64SSASignalSend(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, signalSendSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendSource)\n%s", code, stderr)
	}
}

func TestArm64SSASignalSendDelivers(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, signalSendDeliverySource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendDeliverySource)\n%s", code, stderr)
	}
}

func TestInterpSignalSend(t *testing.T) {
	if code := runInterpExit(t, signalSendSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see signalSendSource)", code)
	}
}

// The wasm answer is a refusal, and it has to arrive from the capability scan
// rather than as an `unknown callee` out of the emitter — the difference
// between "this target has no processes" and "the backend forgot one". Both
// previews, since `proc` is withheld by each.
func TestWASMSignalSendRefused(t *testing.T) {
	prog, err := parser.Parse(signalSendSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted signal_send; it has no process table to deliver to", target)
			continue
		}
		if vs[0].Builtin != "signal_send" || vs[0].Capability != "proc" {
			t.Errorf("%s refused %q on %q, want signal_send on proc", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
