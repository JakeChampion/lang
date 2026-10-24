package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

const procWaitpidNohangSource = `function main(): i32 {
    // No children at all: -ECHILD, the same failure the blocking sibling
    // reports. Not -1, which would claim a child is still running.
    if (proc_waitpid_nohang(0 - 1) != 0 - 10) { return 1; }

    let kid: i32 = proc_fork();
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
    let k2: i32 = proc_fork();
    if (k2 < 0) { return 6; }
    if (k2 == 0) {
        sleep_ms(60000 as i64);
        exit(70);
    }
    match (signal_send(k2, 9)) {
        Ok(_) => {},
        Err(_) => { return 7; }
    }
    let tries: i32 = 0;
    let got: i32 = 0 - 1;
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
