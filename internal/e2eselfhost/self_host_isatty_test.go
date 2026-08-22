package e2eselfhost

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/tty"
)

// `isatty` is reachable from the STDLIB — `std/cli`'s colour gate calls it —
// so the self-hosted compiler has to lower it, not merely be told by the
// capability tables that it is allowed. Before it did, `std/cli` compiled on
// one compiler and not the other, and the driver reported only "module is not
// IR-eligible".
//
// Classification is pinned elsewhere (internal/platforms, internal/caps and
// their self-host mirrors). What this pins is the LOWERING, and it pins it by
// running the result: the self-host-compiled binary must answer no on a pipe
// and yes on a real terminal. A helper that always returned 0 would satisfy
// half of that, which is the half a redirected-output-only test checks.
func TestSelfHostIsattyFollowsTheStream(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	src := filepath.Join(t.TempDir(), "tty.fern")
	if err := os.WriteFile(src, []byte(`function main(): i32 {
    if (isatty(1)) { print("tty"); } else { print("not-tty"); }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	asm, err := exec.Command(mmc, src).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("self-host compile failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("self-host compile failed: %v", err)
	}
	bin := buildBin(t, gcc, dir, "isatty_stream", string(asm))

	var piped bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stdout = &piped
	if err := cmd.Run(); err != nil {
		t.Fatalf("run redirected: %v", err)
	}
	if got := strings.TrimSpace(piped.String()); got != "not-tty" {
		t.Errorf("redirected: got %q, want %q", got, "not-tty")
	}

	master, slave, err := tty.OpenPTY()
	if err != nil {
		t.Fatalf("OpenPTY: %v", err)
	}
	defer master.Close()
	var ttyOut bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&ttyOut, master)
		close(done)
	}()
	cmd = exec.Command(bin)
	cmd.Stdout = slave
	runErr := cmd.Run()
	slave.Close()
	<-done
	if runErr != nil {
		t.Fatalf("run on a pty: %v", runErr)
	}
	if got := strings.TrimSpace(ttyOut.String()); got != "tty" {
		t.Errorf("on a pty: got %q, want %q", got, "tty")
	}
}
