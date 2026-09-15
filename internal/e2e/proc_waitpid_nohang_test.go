// `proc_waitpid_nohang` (#8374) end to end on every backend that provides it.
//
// The primitive answers the question a blocking `proc_waitpid(-1)` loses.
// wait4's negative-pid convention reaps whichever child exits first, which is
// how a supervisor waits on several children with one call — but the builtin
// reports only the STATUS, so the pid is gone. One nohang probe per candidate
// recovers it: a child already reaped answers -ECHILD where a live one answers
// -1. `timeout` is the first caller, using it to tell its command's exit from
// its deadline timer's.
//
// The probe measures each of the three answers, because a lowering that
// dropped WNOHANG would still compile and would still pass anything that only
// waited for a child which does exit:
//
//   - a live child answers -1 rather than blocking, which is the whole point,
//   - -1 rather than 0, which is what wait4 itself returns there and what a
//     clean exit decodes to,
//   - a reaped child answers -ECHILD, which is the identity signal,
//   - a signal death decodes to 128+signal, as the blocking sibling does.
//
// wasm has no leg at all: neither WASI preview has a process model to reap in,
// so the `proc` capability withholds the builtin and E066 refuses the program
// at check time. TestWASMProcWaitpidNohangRefused is that half.
package e2e

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/platforms"
)

// Each failing step returns its own code.
const procWaitpidNohangSource = `function main(): i32 {
    // No children at all: -ECHILD, the same failure the blocking sibling
    // reports. Not -1, which would claim a child is still running.
    if (proc_waitpid_nohang(0 - 1) != 0 - 10) { return 1; }

    var kid: i32 = proc_fork();
    if (kid < 0) { return 2; }
    if (kid == 0) {
        sleep_ms(400 as i64);
        exit(7);
    }
    // The child is alive and has nothing to report, so the call returns
    // rather than blocking — and returns -1, where wait4 itself answers 0
    // and 0 is what a clean exit decodes to.
    if (proc_waitpid_nohang(kid) != 0 - 1) { return 3; }

    // The blocking wait on -1 reaps whoever exits first and reports the
    // status without the pid; the probe below is how the pid comes back.
    if (proc_waitpid(0 - 1) != 7) { return 4; }
    if (proc_waitpid_nohang(kid) != 0 - 10) { return 5; }

    // A signal death decodes to 128+signal, as proc_waitpid does. The kill
    // is asynchronous, so this polls for reapability rather than assuming
    // the child is already gone — which is also the only shape in which a
    // dropped WNOHANG would hang instead of failing.
    var k2: i32 = proc_fork();
    if (k2 < 0) { return 6; }
    if (k2 == 0) {
        sleep_ms(60000 as i64);
        exit(70);
    }
    match (signal_send(k2, 9)) {
        Ok(_) => {},
        Err(_) => { return 7; }
    }
    var tries: i32 = 0;
    var got: i32 = 0 - 1;
    while (tries < 5000) {
        got = proc_waitpid_nohang(k2);
        if (got != 0 - 1) { break; }
        sleep_ms(1 as i64);
        tries = tries + 1;
    }
    if (got != 137) { return 8; }
    return 0;
}
`

func TestX86_64ProcWaitpidNohang(t *testing.T) {
	if code, out := compileRunX86_64WithSetup(t, procWaitpidNohangSource, nil); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see procWaitpidNohangSource)\n%s", code, out)
	}
}

func TestArm64ProcWaitpidNohang(t *testing.T) {
	out, code := compileAndRunArm64(t, procWaitpidNohangSource)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see procWaitpidNohangSource)\n%s", code, out)
	}
}

func TestArm64SSAProcWaitpidNohang(t *testing.T) {
	fern := buildFernForArm64SSA(t)
	qemu := arm64QemuOrEmpty(t)
	bin := compileArm64SSA(t, fern, procWaitpidNohangSource, os.Environ())
	code, stderr := runArm64SSABin(t, qemu, bin, t.TempDir(), os.Environ())
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see procWaitpidNohangSource)\n%s", code, stderr)
	}
}

// The interpreter cannot bare-fork (Go's runtime is threaded), so its whole
// contract is the no-children answer — and it has to be -ECHILD rather than
// -1, or a caller polling for a child that can never exist would spin.
const procWaitpidNohangInterpSource = `function main(): i32 {
    if (proc_waitpid_nohang(0 - 1) != 0 - 10) { return 1; }
    if (proc_waitpid_nohang(1) != 0 - 10) { return 2; }
    return 0;
}
`

func TestInterpProcWaitpidNohang(t *testing.T) {
	if code := runInterpExit(t, procWaitpidNohangInterpSource); code != 0 {
		t.Fatalf("exit = %d, want 0 — the code names the step (see procWaitpidNohangInterpSource)", code)
	}
}

// The wasm answer is a refusal, and it has to arrive from the capability scan
// rather than as an `unknown callee` out of the emitter — the difference
// between "this target has no processes" and "the backend forgot one". Both
// previews, since `proc` is withheld by each.
func TestWASMProcWaitpidNohangRefused(t *testing.T) {
	src := `function main(): i32 { return proc_waitpid_nohang(1); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, target := range []string{"wasm32-wasi", "wasm32-wasi-http"} {
		vs := platforms.Enforce(prog, target)
		if len(vs) == 0 {
			t.Errorf("%s accepted proc_waitpid_nohang; it has no process model to reap in", target)
			continue
		}
		if vs[0].Builtin != "proc_waitpid_nohang" || vs[0].Capability != "proc" {
			t.Errorf("%s refused %q on %q, want proc_waitpid_nohang on proc", target, vs[0].Builtin, vs[0].Capability)
		}
	}
}
