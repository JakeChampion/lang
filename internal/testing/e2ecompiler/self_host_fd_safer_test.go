package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const selfHostFdSaferSrc = `function main(): i32 {
    match (open_writer("alias.out")) {
        Ok(w) => {
            let refused: boolean = false;
            match (stdout().write("TO-STDOUT\n")) {
                Err(e) => { refused = true; },
                Ok(_) => { }
            }
            match (w.write("TO-FILE\n")) {
                Err(e) => { return 4; },
                Ok(_) => { }
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
	run.Stdout = (*os.File)(nil)
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != 0 {
		t.Errorf("with fd 1 closed: exit %d, want 0 (2 = stdout().write went to the opened file)", code)
	}
	if got, err := os.ReadFile(filepath.Join(work, "alias.out")); err != nil || string(got) != "TO-FILE\n" {
		t.Errorf("the opened file holds %q (err %v), want only what was written to it", got, err)
	}
}
