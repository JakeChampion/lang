package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `cli_color_enabled()` is the gate every coloriser in `std/cli` consults.
// It is decided by the environment AND by whether stdout is a terminal, so
// it cannot be pinned by a conformance case (the case format can set
// neither) and it needs the real CLI rather than an in-process check.
//
// Four signals, in precedence order: FORCE_COLOR forces colour on, NO_COLOR
// forces it off, TERM=dumb turns it off, and otherwise stdout must be a
// terminal. FORCE_COLOR wins over NO_COLOR because it is the more
// deliberate signal — NO_COLOR is usually exported once in a profile,
// FORCE_COLOR is set for one run. The tty test comes last because the three
// variables are overrides: a user who exported one has said something more
// specific than the stream shape has.
//
// Every row runs twice, against a pipe and against a real pseudo-terminal,
// because the whole point of the tty clause is that those two disagree when
// nothing else has been said.
func TestCliColorGateHonoursTheConventions(t *testing.T) {
	bin := buildLangBinForInterp(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "gate.fern")
	if err := os.WriteFile(src, []byte(`import "std/cli";
function main(): i32 {
    if (cli.cli_color_enabled()) { return 1; }
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cases := []struct {
		name string
		env  []string
		pipe bool // colour when stdout is a pipe
		tty  bool // colour when stdout is a terminal
	}{
		{"unset", nil, false, true},
		{"NO_COLOR disables", []string{"NO_COLOR=1"}, false, false},
		{"NO_COLOR empty still disables", []string{"NO_COLOR="}, false, false},
		{"TERM=dumb disables", []string{"TERM=dumb"}, false, false},
		{"TERM=xterm allows on a tty only", []string{"TERM=xterm"}, false, true},
		{"FORCE_COLOR forces on", []string{"FORCE_COLOR=1"}, true, true},
		{"FORCE_COLOR beats NO_COLOR", []string{"FORCE_COLOR=1", "NO_COLOR=1"}, true, true},
		{"FORCE_COLOR beats TERM=dumb", []string{"FORCE_COLOR=1", "TERM=dumb"}, true, true},
		{"NO_COLOR beats TERM=xterm", []string{"NO_COLOR=1", "TERM=xterm"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/pipe", func(t *testing.T) {
			if got := runColorGate(t, bin, src, tc.env, false); got != tc.pipe {
				t.Fatalf("env %v on a pipe: colour=%v, want %v", tc.env, got, tc.pipe)
			}
		})
		t.Run(tc.name+"/tty", func(t *testing.T) {
			if got := runColorGate(t, bin, src, tc.env, true); got != tc.tty {
				t.Fatalf("env %v on a tty: colour=%v, want %v", tc.env, got, tc.tty)
			}
		})
	}
}

// runColorGate runs the gate program with the given environment, wiring its
// stdout either to a pipe or to a real pty, and reports whether it decided
// colour was on (exit status 1).
//
// The base environment is deliberately minimal: a CI runner's own TERM or
// NO_COLOR would otherwise decide the row instead of the row deciding it.
func runColorGate(t *testing.T, bin, src string, env []string, tty bool) bool {
	t.Helper()
	cmd := exec.Command(bin, "-interp", src)
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if !tty {
		cmd.Stdout = io.Discard
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode() == 1
	}
	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer master.Close()
	cmd.Stdout = slave
	// Drain the master while the child writes: a pty buffer is small
	// enough that an undrained one can block the writer.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, master)
		close(done)
	}()
	runErr := cmd.Run()
	slave.Close()
	<-done
	if runErr != nil && cmd.ProcessState == nil {
		t.Fatalf("run on a pty: %v\nstderr: %s", runErr, errb.String())
	}
	return cmd.ProcessState.ExitCode() == 1
}

// The gate above proves the DECISION; this proves the decision reaches the
// bytes, through the native `__fern_isatty` helper rather than Go's. A
// coloured CLI must emit SGR escapes to a terminal and none at all when its
// output is redirected — the redirect case being the bug `isatty` exists to
// fix (#6387), where every other ecosystem treats "stdout is not a
// terminal" as the primary signal and Fern had only the overrides.
//
// Both native backends run it: each grew its own ioctl-based helper, and a
// helper that answers "no" unconditionally would pass a test that only
// checked the redirected half.
func TestColouredCliStripsEscapesWhenRedirected(t *testing.T) {
	const src = `import "std/cli";
function main(): i32 {
    print(cli.cli_red("boom"));
    return 0;
}`
	t.Run("x86-64", func(t *testing.T) {
		bin, runner := compileX86_64Bin(t, src)
		assertColourFollowsTheStream(t, func() *exec.Cmd { return runX86_64Bin(runner, bin) })
	})
	t.Run("arm64", func(t *testing.T) {
		bin, qemu := compileArm64Bin(t, src)
		assertColourFollowsTheStream(t, func() *exec.Cmd { return runArm64Bin(qemu, bin) })
	})
}

// assertColourFollowsTheStream runs the coloured program twice — redirected
// and on a pty — and requires plain text from the first and SGR escapes from
// the second. `newCmd` builds a fresh command each time because a *exec.Cmd
// cannot be reused.
func assertColourFollowsTheStream(t *testing.T, newCmd func() *exec.Cmd) {
	t.Helper()
	// TERM is set so the run cannot pass for the wrong reason: with TERM
	// unset the tty leg would still be expected to colour, but the
	// redirected leg would have two independent reasons to be plain.
	env := []string{"PATH=" + os.Getenv("PATH"), "TERM=xterm"}

	var piped bytes.Buffer
	cmd := newCmd()
	cmd.Env = env
	cmd.Stdout = &piped
	if err := cmd.Run(); err != nil {
		t.Fatalf("run redirected: %v", err)
	}
	if got := piped.String(); strings.ContainsRune(got, 0x1b) || strings.TrimSpace(got) != "boom" {
		t.Fatalf("redirected output = %q, want plain %q", got, "boom\n")
	}

	master, slave, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY: %v", err)
	}
	defer master.Close()
	var ttyOut bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&ttyOut, master)
		close(done)
	}()
	cmd = newCmd()
	cmd.Env = env
	cmd.Stdout = slave
	runErr := cmd.Run()
	slave.Close()
	<-done
	if runErr != nil {
		t.Fatalf("run on a pty: %v", runErr)
	}
	if got := ttyOut.String(); !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[0m") {
		t.Fatalf("tty output = %q, want the SGR pair around %q", got, "boom")
	}
}
