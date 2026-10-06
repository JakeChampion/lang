package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type StatByteCase struct {
	Name, Format, Want, Warning string
}

const StatByteFilename = "é界𐀀"

func StatByteCases() []StatByteCase {
	var hex, oct strings.Builder
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
		fmt.Fprintf(&hex, `\x%02x`, i)
		fmt.Fprintf(&oct, `\%03o`, i)
	}
	cases := []StatByteCase{
		{Name: "all hex bytes", Format: hex.String(), Want: string(all)},
		{Name: "all octal bytes", Format: oct.String(), Want: string(all)},
		{Name: "unicode", Format: StatByteFilename, Want: StatByteFilename},
		{Name: "mixed numeric raw name", Format: "%s " + hex.String() + " %.1n", Want: "3 " + string(all) + " \xc3"},
		{Name: "buffer boundary", Format: strings.Repeat(hex.String(), 64), Want: strings.Repeat(string(all), 64)},
		{Name: "unknown escaped Unicode", Format: "\\é", Want: "é", Warning: "warning: unrecognized escape '\\\xc3'\n"},
		{Name: "unknown escaped three bytes", Format: "\\界", Want: "界", Warning: "warning: unrecognized escape '\\\xe7'\n"},
		{Name: "unknown escaped four bytes", Format: "\\𐀀", Want: "𐀀", Warning: "warning: unrecognized escape '\\\xf0'\n"},
		{Name: "terminal backslash", Format: `\`, Want: `\`, Warning: "warning: backslash at end of format\n"},
		{Name: "hex without digits", Format: `\x`, Want: "x", Warning: "warning: unrecognized escape '\\x'\n"},
	}
	for _, scalar := range []string{"é", "界", "𐀀"} {
		cases = append(cases, StatByteCase{Name: "unknown directive " + scalar, Format: "%" + scalar, Want: "?" + scalar[1:]},
			StatByteCase{Name: "unknown modifier " + scalar, Format: "%H" + scalar, Want: "?" + scalar})
	}
	for n := 0; n <= len(StatByteFilename); n++ {
		cases = append(cases, StatByteCase{Name: fmt.Sprintf("precision %d", n), Format: fmt.Sprintf("%%.%dn", n), Want: StatByteFilename[:n]})
	}
	cases = append(cases,
		StatByteCase{Name: "left padding partial scalar", Format: "%8.1n", Want: "       \xc3"},
		StatByteCase{Name: "right padding partial scalar", Format: "%-8.1n", Want: "\xc3       "})
	return cases
}

func RunStatByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StatByteFilename), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range StatByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			argv := append(append(append([]string{}, runner...), bin), "--printf", tc.Format, StatByteFilename)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "LC_ALL=C")
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.String())
			}
			if out.String() != tc.Want {
				t.Fatalf("stdout: got %q, want %q", out.String(), tc.Want)
			}
			var warnings string
			for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
				if _, warning, ok := strings.Cut(line, ": warning: "); ok {
					warnings += "warning: " + warning
				}
			}
			if warnings != tc.Warning {
				t.Fatalf("warnings: got %q, want %q", warnings, tc.Warning)
			}
			if census != nil {
				census(t, diagnostic.String())
			}
		})
	}
	// Device nodes exercise Darwin's mount-point fallback when pathconf
	// rejects the operand. Keep success, failure and continuation under the
	// same ownership checks as byte formatting.
	if err := os.Symlink("/dev/null", filepath.Join(dir, "device-link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{"filesystem file", []string{StatByteFilename}, StatByteFilename, 0},
		{"filesystem device", []string{"/dev/null"}, "/dev/null", 0},
		{"filesystem device link", []string{"device-link"}, "device-link", 0},
		{"filesystem missing", []string{"missing"}, "", 1},
		{"filesystem continuation", []string{"/dev/null", "missing", "device-link"}, "/dev/nulldevice-link", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			argv := append(append(append([]string{}, runner...), bin), "-f", "--printf", "%n")
			argv = append(argv, tc.args...)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "LC_ALL=C")
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			code := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatalf("run: %v\n%s", err, diagnostic.String())
				}
			}
			if code != tc.code || out.String() != tc.want {
				t.Fatalf("got exit %d, stdout %q; want exit %d, stdout %q\n%s", code, out.String(), tc.code, tc.want, diagnostic.String())
			}
			if tc.code != 0 && !strings.Contains(diagnostic.String(), "cannot read file system information for") {
				t.Fatalf("missing filesystem diagnostic: %s", diagnostic.String())
			}
			if census != nil {
				census(t, diagnostic.String())
			}
		})
	}
}
