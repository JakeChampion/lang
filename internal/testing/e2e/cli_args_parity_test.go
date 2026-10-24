package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const argsEchoProgram = `function main(): i32 {
    let av: string[] = args();
    print("argv0=" + av[0]);
    let i: i32 = 1;
    while (i < av.len()) {
        print("[" + av[i] + "]");
        i = i + 1;
    }
    print("end");
    return 0;
}
`

func TestArgsParityInterpVsCompiled(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(argsEchoProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	tails := []struct {
		name string
		tail []string
		want []string
	}{
		{name: "empty", tail: nil, want: nil},
		{name: "plain", tail: []string{"a", "b", "c"}, want: []string{"[a]", "[b]", "[c]"}},
		{name: "separator", tail: []string{"--", "a", "b"}, want: []string{"[a]", "[b]"}},
		{name: "flaglike", tail: []string{"--", "-x", "--verbose"}, want: []string{"[-x]", "[--verbose]"}},
		{name: "literal_separator", tail: []string{"--", "--", "x"}, want: []string{"[--]", "[x]"}},
	}

	type engine struct {
		name  string
		run   func(t *testing.T, tail []string) string
		argv0 func(t *testing.T, got string)
	}
	engines := []engine{{
		name: "interp",
		run: func(t *testing.T, tail []string) string {
			return runFernProgram(t, bin, append([]string{"-interp", src}, tail...))
		},
		argv0: func(t *testing.T, got string) {
			if got != src {
				t.Errorf("interp args()[0] = %q, want the source path %q", got, src)
			}
		},
	}}

	compiledArgv0 := func(t *testing.T, got string) {
		if got == "" || strings.HasSuffix(got, ".fern") {
			t.Errorf("compiled args()[0] = %q, want the executable's path", got)
		}
	}
	runnable := 0
	for _, ct := range []struct {
		target string
		runner func() (string, bool)
	}{
		{"x86-64-linux", x86Runner},
		{"arm64-linux", arm64Runner},
	} {
		_, ok := ct.runner()
		if ok {
			runnable++
		}
		target := ct.target
		engines = append(engines, engine{
			name: target,
			run: func(t *testing.T, tail []string) string {
				if !ok {
					t.Skipf("no way to run %s binaries on this host", target)
				}
				return runFernProgram(t, bin, append([]string{"-run", "-target", target, src}, tail...))
			},
			argv0: compiledArgv0,
		})
	}
	if runnable == 0 {
		t.Skipf("no register target is runnable on %s/%s: nothing to compare -interp against", runtime.GOOS, runtime.GOARCH)
	}

	for _, tc := range tails {
		t.Run(tc.name, func(t *testing.T) {
			var reference []string
			var referenceEngine string
			for _, eng := range engines {
				t.Run(eng.name, func(t *testing.T) {
					out := eng.run(t, tc.tail)
					argv0, rest := splitArgsEcho(t, out)
					eng.argv0(t, argv0)
					if !equalStrings(rest, tc.want) {
						t.Errorf("%s args()[1:] = %q, want %q", eng.name, rest, tc.want)
					}
					if reference == nil {
						reference, referenceEngine = rest, eng.name
						return
					}
					if !equalStrings(rest, reference) {
						t.Errorf("args()[1:] differs between engines: %s = %q, %s = %q",
							eng.name, rest, referenceEngine, reference)
					}
				})
			}
		})
	}
}

func TestFlagAfterFileIsRefusedByBothEngines(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(src, []byte(argsEchoProgram), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog.bin")

	for _, argv := range [][]string{
		{src, "-o", out},
		{"-interp", src, "-o", out},
		{"-run", "-target", "x86-64-linux", src, "-o", out},
	} {
		cmd := exec.Command(bin, argv...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 2 {
			t.Errorf("fern %s exit = %d, want 2\nstdout (first lines):\n%s", strings.Join(argv, " "), code, firstNLines(stdout.String(), 3))
		}
		if !strings.Contains(stderr.String(), "-o comes after the source file") {
			t.Errorf("fern %s stderr does not explain the misplaced flag:\n%s", strings.Join(argv, " "), stderr.String())
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("fern %s wrote %s even though it was refused", strings.Join(argv, " "), out)
		}
	}
}

func runFernProgram(t *testing.T, bin string, argv []string) string {
	t.Helper()
	cmd := exec.Command(bin, argv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fern %s: %v\nstderr:\n%s", strings.Join(argv, " "), err, stderr.String())
	}
	return stdout.String()
}

func splitArgsEcho(t *testing.T, out string) (string, []string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "argv0=") || lines[len(lines)-1] != "end" {
		t.Fatalf("unexpected program output:\n%s", out)
	}
	return strings.TrimPrefix(lines[0], "argv0="), lines[1 : len(lines)-1]
}

func firstNLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
