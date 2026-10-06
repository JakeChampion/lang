package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// RunDircolorsByteCases pins GNU's raw output independently of its presence
// on the target. The native differential corpus also checks these behaviors.
func RunDircolorsByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	cases := []struct{ name, input, shell, ls, diagnostic string }{
		{"extension", ".\xff 01;31\n", "*.\xff=01;31:", "\x1b[01;31m*.\xff\t01;31\x1b[0m\n", ""},
		{"value", "DIR \xff\x80\n", "di=\xff\x80:", "\x1b[\xff\x80mdi\t\xff\x80\x1b[0m\n", ""},
		{"keyword", "TERM *\n\xff\x80 1\n", "", "", "-:2: unrecognized keyword \xff\x80\n"},
		{"missing value", "TERM *\n\xff\x80\n", "", "", "-:2: invalid line;  missing second token\n"},
		{"nul", "DIR \xff\x00ignored\nLINK \x80\n", "di=\xff:ln=\x80:", "\x1b[\xffmdi\t\xff\x1b[0m\n\x1b[\x80mln\t\x80\x1b[0m\n", ""},
		{"escapes", ".\xff' a\xff:b=\x80^:\\:\n", "*.\xff'\\''=a\xff\\:b\\=\x80^:\\::", "\x1b[a\xff:b=\x80^:\\:m*.\xff'\ta\xff:b=\x80^:\\:\x1b[0m\n", ""},
		{"class", "TERM [![:\xff:]]\nDIR 1\nTERM linux\nLINK 2\n", "ln=2:", "\x1b[2mln\t2\x1b[0m\n", ""},
	}
	high := make([]byte, 128)
	for i := range high {
		high[i] = byte(i + 128)
	}
	for _, n := range []int{0, 4094, 4095, 4096, 8191, 8192, 65534, 65535, 65536} {
		value := strings.Repeat("x", n) + "€" + string(high)
		cases = append(cases, struct{ name, input, shell, ls, diagnostic string }{
			fmt.Sprintf("boundary%d", n), "DIR " + value + "\n", "di=" + value + ":", "\x1b[" + value + "mdi\t" + value + "\x1b[0m\n", "",
		})
	}
	for _, tc := range cases {
		for _, mode := range []string{"-b", "-c", "--print-ls-colors"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				want := "LS_COLORS='" + tc.shell + "';\nexport LS_COLORS\n"
				if mode == "-c" {
					want = "setenv LS_COLORS '" + tc.shell + "'\n"
				} else if mode == "--print-ls-colors" {
					want = tc.ls
				}
				status := 0
				if tc.diagnostic != "" {
					want, status = "", 1
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				argv := append(append([]string{}, runner...), bin, mode, "-")
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Env = ChildEnv("TERM=linux", "COLORTERM=", "LC_ALL=C")
				cmd.Stdin = strings.NewReader(tc.input)
				var out, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostic
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != status || out.String() != want {
					t.Fatalf("run: %v; got %d output bytes, want %d; stderr=%s", err, out.Len(), len(want), diagnostic.Bytes())
				}
				if census != nil {
					census(t, diagnostic.String())
				}
				var messages string
				for _, line := range strings.Split(diagnostic.String(), "\n") {
					if line == "" || strings.HasPrefix(line, "leakcheck:") {
						continue
					}
					_, message, ok := strings.Cut(line, ": ")
					if !ok {
						t.Fatalf("unexpected diagnostic: %q", line)
					}
					messages += message + "\n"
				}
				if messages != tc.diagnostic {
					t.Fatalf("diagnostic=%q; want %q", messages, tc.diagnostic)
				}
			})
		}
	}
}
