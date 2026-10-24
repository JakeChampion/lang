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
		pipe bool
		tty  bool
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

func assertColourFollowsTheStream(t *testing.T, newCmd func() *exec.Cmd) {
	t.Helper()
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
