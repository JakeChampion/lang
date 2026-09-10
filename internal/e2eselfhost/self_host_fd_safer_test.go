package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The self-host half of #8823: __fern_open_res (asmcore.rt_src_open_res)
// moves a descriptor below 3 up with fcntl F_DUPFD before wrapping it, so a
// file opened while fd 1 is closed is never handed descriptor 1 and
// stdout().write cannot land in it. Same program as the native gate
// (internal/e2e/fd_safer_test.go): exit 2 means the stdout write "succeeded"
// into the file, and the file must hold only what was written to it.
const selfHostFdSaferSrc = `function main(): i32 {
    match (open_writer("alias.out")) {
        Ok(w) => {
            var refused: boolean = false;
            match (stdout().write("TO-STDOUT\n")) {
                Some(e) => { refused = true; },
                None => { }
            }
            match (w.write("TO-FILE\n")) {
                Some(e) => { return 4; },
                None => { }
            }
            w.close();
            if (!refused) { return 2; }
            return 0;
        },
        Err(e) => { return 3; }
    }
}
`

func TestSelfHostOpenedHandleNeverLandsOnStdoutX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	src := filepath.Join(dir, "fd_safer.fern")
	if err := os.WriteFile(src, []byte(selfHostFdSaferSrc), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-emit", "asm", src)
	asm, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("self-host compile: %v\n%s", err, stderr)
	}
	bin := buildBin(t, gcc, dir, "fd_safer", string(asm))

	work := t.TempDir()
	run := exec.Command(bin)
	run.Dir = work
	// A typed nil *os.File leaves fd 1 CLOSED in the child, as `prog >&-`
	// does, rather than on /dev/null.
	run.Stdout = (*os.File)(nil)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("with fd 1 closed: exit %d, want 0 (2 = stdout().write went to the opened file)", code)
	}
	if got, err := os.ReadFile(filepath.Join(work, "alias.out")); err != nil || string(got) != "TO-FILE\n" {
		t.Errorf("the opened file holds %q (err %v), want only what was written to it", got, err)
	}
}
