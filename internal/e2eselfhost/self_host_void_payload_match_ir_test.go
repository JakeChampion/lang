package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestSelfHostVoidPayloadMatchIR pins a `match` whose arm binds a VOID payload
// by name — `Ok(u)` on a `Result[void, IoError]` — on the self-host IR path.
//
// Every fallible filesystem builtin that returns no value has this shape
// (write_file, remove_file, create_dir_all, remove_dir_all), so the arm is
// ordinary source. It bailed the whole module to the AST emitter, because the
// arm's payload-type gate accepted every payload spelling except the one that
// carries nothing: `void` fell through to `s.fail()`. A `_` binder took a
// different path and lowered, which is why the existing filesystem IR tests —
// written with `Ok(_)` throughout — never saw it.
//
// The driver runs under FERN_STRICT_IR=1: a bail is then a hard error naming
// the function rather than a silent fall-through, so a regression says which
// arm left the subset.
func TestSelfHostVoidPayloadMatchIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	// Each arm names its unit binder, and the exit code identifies the step
	// that failed. The last pair mixes a named binder with a `_` one, so the
	// two payload paths are exercised against the same builtin.
	const src = `function main(): i32 {
    match (temp_dir("fern-voidpayload-ir")) {
        Ok(d) => {
            match (create_dir_all(d + "/a/b")) { Ok(u) => {}, Err(e) => { return 1; }, }
            match (write_file(d + "/a/b/x.txt", "twelve")) { Ok(u) => {}, Err(e) => { return 2; }, }
            match (read_file(d + "/a/b/x.txt")) {
                Ok(s) => { if (s != "twelve") { return 3; } },
                Err(e) => { return 4; },
            }
            match (remove_file(d + "/a/b/x.txt")) { Ok(u) => {}, Err(e) => { return 5; }, }
            match (read_file(d + "/a/b/x.txt")) { Ok(s) => { return 6; }, Err(e) => {}, }
            match (write_file(d + "/a/b/y.txt", "y")) { Ok(_) => {}, Err(e) => { return 7; }, }
            match (remove_dir_all(d)) { Ok(u) => {}, Err(e) => { return 8; }, }
            return 0;
        },
        Err(e) => { return 9; },
    }
}`

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	cmd.Env = append(cmd.Environ(), "FERN_STRICT_IR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v\n%s", err, stderr.String())
	}

	progBin := buildBin(t, gcc, dir, "voidpayload_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("void-payload match program exited %d, want 0 — the code names the failing step", code)
	}
}
