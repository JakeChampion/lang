package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// argsEchoProgram prints its own argv: slot 0 on its own line (it differs by
// design between engines — the source path under -interp, the executable for a
// compiled binary), then every later slot bracketed so empty strings and
// trailing whitespace stay visible, then a terminator so "no arguments" is
// distinguishable from "produced no output".
const argsEchoProgram = `function main(): i32 {
    var av: string[] = args();
    print("argv0=" + av[0]);
    var i: i32 = 1;
    while (i < av.len()) {
        print("[" + av[i] + "]");
        i = i + 1;
    }
    print("end");
    return 0;
}
`

// TestArgsParityInterpVsCompiled pins the contract that `args()[1:]` is the
// same vector under -interp and in a compiled binary for the same command tail
// (#8465): the interp path used to strip a `--` separator that the compile
// path forwarded to the program verbatim, so `fern -interp p.fern -- --filter x`
// and `fern --run p.fern -- --filter x` disagreed. -interp is the reference
// oracle for the backends, so a divergence here tests stdlib argument parsing
// against one convention and ships it under another.
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
		want []string // args()[1:], one bracketed line each
	}{
		{name: "empty", tail: nil, want: nil},
		{name: "plain", tail: []string{"a", "b", "c"}, want: []string{"[a]", "[b]", "[c]"}},
		{name: "separator", tail: []string{"--", "a", "b"}, want: []string{"[a]", "[b]"}},
		{name: "flaglike", tail: []string{"--", "-x", "--verbose"}, want: []string{"[-x]", "[--verbose]"}},
		{name: "literal_separator", tail: []string{"--", "--", "x"}, want: []string{"[--]", "[x]"}},
	}

	// Each engine reports what it ran as, so the argv0 line can be checked
	// against the right expectation while args()[1:] is compared across all
	// of them.
	engines := []struct {
		name string
		// run returns the program's stdout for one command tail.
		run func(t *testing.T, tail []string) string
		// argv0 checks the engine's own argv[0] convention.
		argv0 func(t *testing.T, got string)
	}{
		{
			name: "interp",
			run: func(t *testing.T, tail []string) string {
				return runFernProgram(t, bin, append([]string{"-interp", src}, tail...))
			},
			argv0: func(t *testing.T, got string) {
				if got != src {
					t.Errorf("interp args()[0] = %q, want the source path %q", got, src)
				}
			},
		},
		{
			name: "x86-64",
			run: func(t *testing.T, tail []string) string {
				return runFernProgram(t, bin, append([]string{"--run", "-target", "x86-64-linux", src}, tail...))
			},
			argv0: func(t *testing.T, got string) {
				if got == "" || strings.HasSuffix(got, ".fern") {
					t.Errorf("compiled args()[0] = %q, want the executable's path", got)
				}
			},
		},
		{
			name: "arm64",
			run: func(t *testing.T, tail []string) string {
				if _, ok := arm64Runner(); !ok {
					t.Skip("no qemu-aarch64 to run arm64 binaries")
				}
				return runFernProgram(t, bin, append([]string{"--run", "-target", "arm64-linux", src}, tail...))
			},
			argv0: func(t *testing.T, got string) {
				if got == "" || strings.HasSuffix(got, ".fern") {
					t.Errorf("compiled args()[0] = %q, want the executable's path", got)
				}
			},
		},
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

// A driver flag written after FILE reaches neither the driver nor the program:
// `fern p.fern -o out` used to print assembly to stdout and exit 0 with no
// binary written. Both engines must refuse it, and identically (#8465).
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
		{"--run", "-target", "x86-64-linux", src, "-o", out},
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

// runFernProgram runs the fern CLI with argv and returns the program's stdout,
// failing the test on a non-zero exit.
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

// splitArgsEcho parses argsEchoProgram's output into its argv[0] and the
// bracketed lines for args()[1:].
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
