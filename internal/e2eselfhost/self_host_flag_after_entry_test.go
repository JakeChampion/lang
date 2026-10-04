package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFlagAfterEntryIsRefused pins the driver's positional rule to
// native's (#11406, #8465): driver flags end at the entry file, a `-x` written
// after it is refused with exit 2 rather than parsed or handed to the program
// unseen, and the program's own `-x` arguments go after a `--` separator.
func TestSelfHostFlagAfterEntryIsRefused(t *testing.T) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `function main(): i32 {
  let av: string[] = args();
  let i: i32 = 1;
  while (i < av.len()) {
    print(av[i]);
    i = i + 1;
  }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "prog.bin")

	for _, argv := range [][]string{
		{src, "-o", out, cli.stdlib},
		{src, cli.stdlib, "-o", out},
		{"-interp", src, cli.stdlib, "-o", out},
		{"-target", "x86-64-linux", src, cli.stdlib, "-o", out},
	} {
		cmd := runX86_64Bin(cli.runner, cli.bin, argv...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 2 {
			t.Errorf("fern %s: exit %d, want 2\nstdout:\n%s", strings.Join(argv, " "), code, stdout.String())
		}
		if !strings.Contains(stderr.String(), "fern: -o comes after the source file, where driver flags are no longer parsed") {
			t.Errorf("fern %s: stderr does not name the misplaced flag:\n%s", strings.Join(argv, " "), stderr.String())
		}
		if _, err := os.Stat(out); err == nil {
			t.Errorf("fern %s: wrote %s although it was refused", strings.Join(argv, " "), out)
		}
	}

	// The program's own arguments: `--` separates a leading `-x`, a later one
	// is ordinary data, and a bare `-` is a filename, not a flag.
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"-interp", src, cli.stdlib, "--", "-x", "y"}, "-x\ny\n"},
		{[]string{"-interp", src, cli.stdlib, "a", "-x"}, "a\n-x\n"},
		{[]string{"-interp", src, cli.stdlib, "-", "--"}, "-\n--\n"},
		{[]string{"-interp", src, cli.stdlib, "--", "--", "z"}, "--\nz\n"},
	} {
		cmd := runX86_64Bin(cli.runner, cli.bin, tc.argv...)
		got, err := cmd.Output()
		if err != nil {
			t.Errorf("fern %s: %v", strings.Join(tc.argv, " "), err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("fern %s: program saw %q, want %q", strings.Join(tc.argv, " "), got, tc.want)
		}
	}
}
