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

type PrintfByteCase struct {
	Name, Want, Diagnostic string
	Args                   []string
	Code                   int
}

func PrintfByteCases() []PrintfByteCase {
	var hex, octal, bOctal strings.Builder
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
		fmt.Fprintf(&hex, `\x%02x`, i)
		fmt.Fprintf(&octal, `\%03o`, i)
		fmt.Fprintf(&bOctal, `\0%03o`, i)
	}
	cases := []PrintfByteCase{
		{Name: "all hexadecimal bytes", Args: []string{hex.String()}, Want: string(all)},
		{Name: "all octal bytes", Args: []string{octal.String()}, Want: string(all)},
		{Name: "all b octal bytes", Args: []string{"%b", bOctal.String()}, Want: string(all)},
		{Name: "all b hex bytes", Args: []string{"%b", hex.String()}, Want: string(all)},
		{Name: "unknown Unicode format escape", Args: []string{"\\é\\€\\𐀀"}, Want: "\\é\\€\\𐀀"},
		{Name: "unknown Unicode b escape", Args: []string{"%b", "\\é\\€\\𐀀"}, Want: "\\é\\€\\𐀀"},
		{Name: "byte characters across format reuse", Args: []string{"%c", "é", "€", "𐀀"}, Want: "\xc3\xe2\xf0"},
		{Name: "missing character", Args: []string{"%c"}, Want: "\x00"},
		{Name: "empty character", Args: []string{"%c", ""}, Want: "\x00"},
		{Name: "character padding", Args: []string{"|%5c|%-5c|", "é", "€"}, Want: "|    \xc3|\xe2    |"},
		{Name: "early c after raw bytes", Args: []string{"%b%s", `\xff\cignored`, "ignored"}, Want: "\xff"},
		{Name: "early c in format", Args: []string{`\x80\cignored`}, Want: "\x80"},
		{Name: "invalid Unicode conversion", Args: []string{"prefix%é"}, Want: "prefix", Diagnostic: "%\xc3: invalid conversion specification\n", Code: 1},
		{Name: "raw character warning", Args: []string{"%d", "'é"}, Want: "195", Diagnostic: "warning: \xa9: character(s) following character constant have been ignored\n"},
		{Name: "raw float character warning", Args: []string{"%.0f", "'€"}, Want: "226", Diagnostic: "warning: \x82\xac: character(s) following character constant have been ignored\n"},
		{Name: "writer boundary", Args: []string{strings.Repeat(hex.String(), 64)}, Want: strings.Repeat(string(all), 64)},
	}
	text := "é€𐀀"
	for n := 0; n <= len(text); n++ {
		padding := ""
		if n < 5 {
			padding = strings.Repeat(" ", 5-n)
		}
		cases = append(cases,
			PrintfByteCase{Name: fmt.Sprintf("precision %d right", n), Args: []string{fmt.Sprintf("|%%5.%ds|", n), text}, Want: "|" + padding + text[:n] + "|"},
			PrintfByteCase{Name: fmt.Sprintf("precision %d left", n), Args: []string{fmt.Sprintf("|%%-5.%ds|", n), text}, Want: "|" + text[:n] + padding + "|"})
	}
	return cases
}

func RunPrintfByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	for _, tc := range PrintfByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			argv := append(append(append([]string{}, runner...), bin), tc.Args...)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Env = append(os.Environ(), "LC_ALL=C", "POSIXLY_CORRECT=")
			// POSIXLY_CORRECT is tested by presence, so remove it entirely.
			var env []string
			for _, v := range cmd.Env {
				if !strings.HasPrefix(v, "POSIXLY_CORRECT=") {
					env = append(env, v)
				}
			}
			cmd.Env = env
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.Code {
				t.Fatalf("run: %v, state %v\n%s", err, cmd.ProcessState, diagnostic.String())
			}
			if out.String() != tc.Want {
				t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(tc.Want))
			}
			if census != nil && tc.Code == 0 {
				census(t, diagnostic.String())
			}
			var clean []string
			for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
				if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
					if tc.Code != 0 {
						t.Log(strings.TrimSpace(line))
					}
					continue
				}
				clean = append(clean, line)
			}
			got := strings.ReplaceAll(strings.Join(clean, ""), bin+": ", "printf: ")
			got = strings.ReplaceAll(got, filepath.Base(bin)+": ", "printf: ")
			want := tc.Diagnostic
			if want != "" {
				want = "printf: " + want
			}
			if got != want {
				t.Fatalf("stderr differs: got %q, want %q", got, want)
			}
		})
	}
}
