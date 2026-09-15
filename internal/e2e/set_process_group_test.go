// `set_process_group` (#8374) end to end on every backend that provides it.
//
// Two programs. The first asks only for outcomes the runner gets the same
// answer to whoever it runs as: a process that is not a session leader may
// always move into a group of its own, a pid above the kernel's ceiling is
// always ESRCH, a negative pgid is always EINVAL, and a pgid naming no group
// in this session is always EPERM.
//
// The two argument-order errors differ, which is what catches the operands
// arriving transposed: setpgid(4194304, 0) is ESRCH where setpgid(0, 4194304)
// is EPERM, so a lowering that swaps them fails both arms rather than neither.
//
// The second forks and moves the child into a group of its own, then signals
// THE GROUP — a Result that classifies errnos correctly still proves nothing
// about the group having been made. It has no interpreter leg: the interpreter
// cannot bare-fork (Go's runtime is threaded).
//
// wasm has no leg at all: neither WASI preview has a process table to hold a
// group, so the `proc` capability withholds the builtin and E066 refuses the
// program at check time. TestWASMSetProcessGroupRefused is that half.
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
// can ever hold it and no group can ever be named by it — where a number
// merely unused right now could be reassigned between the compile and the run.
const setProcessGroupSource = `function main(): i32 {
    // pid 0 is the caller and pgid 0 is the pid's own value, so this is a
    // process moving into a fresh group of its own. The test binary is a
    // child of the runner rather than a session leader, which is the one
    // case setpgid(2) refuses.
    match (set_process_group(0, 0)) {
        Ok(_) => {},
        Err(_) => { return 1; }
    }

    // Above the kernel's own pid ceiling: ESRCH. None of setpgid(2)'s four
    // errnos has a named IoError variant, so each arrives as
    // Other(path, strerror) — and the path is empty, because the primitive
    // took two integers and never saw a file.
    match (set_process_group(4194304, 0)) {
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

    // A negative pgid: EINVAL.
    match (set_process_group(0, 0 - 1)) {
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

    // A pgid that names no group in this session: EPERM. The same number as
    // the ESRCH arm, on the other side of the call — so an emit that
    // transposes the two operands reports the wrong errno here as well.
    match (set_process_group(0, 4194304)) {
        Ok(_) => { return 10; },
        Err(e) => {
            match (e) {
                Other(p, msg) => {
                    if (msg != "Operation not permitted") { return 11; }
                },
                _ => { return 12; }
            }
        }
    }
    return 0;
}
`

// The group-creation proof. The child sleeps far longer than the parent needs,
// so the only way it reaches the parent's waitpid is a signal delivered to the
// group it joined — and the parent is not in that group, so a kill that landed
// on the caller's own group instead would take the parent down with it.
const setProcessGroupDeliverySource = `function main(): i32 {
    var kid: i32 = proc_fork();
    if (kid < 0) { return 1; }
    if (kid == 0) {
        // The child's half: a group of its own, whose id is its own pid.
        match (set_process_group(0, 0)) {
            Ok(_) => {},
            Err(_) => { exit(71); }
        }
        sleep_ms(60000 as i64);
        // Only reachable if nothing was delivered. A distinct code so a child
        // that outlives the sleep is not read as a clean exit.
        exit(70);
    }
    // The parent's half. Both sides make the call so that neither ordering
    // leaves the child ungrouped; nothing execs here, so this one succeeds.
    match (set_process_group(kid, kid)) {
        Ok(_) => {},
        Err(_) => { return 2; }
    }
    // A negative pid names a process GROUP to kill(2), and this group exists
    // only because the two calls above made it.
    match (signal_send(0 - kid, 9)) {
        Ok(_) => {},
        Err(_) => { return 3; }
    }
    // proc_waitpid reports a signal death as 128+signal.
    var status: i32 = proc_waitpid(kid);
    if (status != 137) { return 4; }
    return 0;
}
`

func TestX86_64SetProcessGroup(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, setProcessGroupSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupSource)\n%s", code, out)
	}
}

func TestX86_64SetProcessGroupMakesTheGroup(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, setProcessGroupDeliverySource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupDeliverySource)\n%s", code, out)
	}
}

func TestArm64SetProcessGroup(t *testing.T) {
	out, code := compileAndRunArm64(t, setProcessGroupSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupSource)\n%s", code, out)
	}
}

func TestArm64SetProcessGroupMakesTheGroup(t *testing.T) {
	out, code := compileAndRunArm64(t, setProcessGroupDeliverySource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupDeliverySource)\n%s", code, out)
	}
}

func TestArm64SSASetProcessGroup(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, setProcessGroupSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupSource)\n%s", code, stderr)
	}
}

func TestArm64SSASetProcessGroupMakesTheGroup(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, setProcessGroupDeliverySource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupDeliverySource)\n%s", code, stderr)
	}
}

func TestInterpSetProcessGroup(t *testing.T) {
	if code := runInterpExit(t, setProcessGroupSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see setProcessGroupSource)", code)
	}
}

// The wasm answer is a refusal, and it has to arrive from the capability scan
// rather than as an `unknown callee` out of the emitter — the difference
// between "this target has no process groups" and "the backend forgot one".
// Both previews, since `proc` is withheld by each.
func TestWASMSetProcessGroupRefused(t *testing.T) {
	prog, err := parser.Parse(setProcessGroupSource)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted set_process_group; it has no process table to hold a group", target)
			continue
		}
		if vs[0].Builtin != "set_process_group" || vs[0].Capability != "proc" {
			t.Errorf("%s refused %q on %q, want set_process_group on proc", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
