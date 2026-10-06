package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type DateByteCase struct {
	Name, Input string
	File, Debug bool
	InvalidZone bool
}

func DateByteCases() []DateByteCase {
	inputs := []string{
		"", "2024-01-02\n", "2024-01-02", "FOO\xffBAR\n2024-01-02\n",
		"2024-01-02\x00\xff\n", "2024-01-02 (\xff\x80\xed\xa0\x80)\n",
		"2024-01-02 (nested (\xe2\x82) \xff)\n", "2024-01-02 (\xff unterminated",
		"2024-01-02 é🙂\n", "FOOé🙂\n", "\xff\n\xc0\xaf\n\xed\xa0\x80\n\xe2\x82\n\x80\n",
		"\tTZ=\"UTC0\" 2024-01-02\r\n", "TZ=\"Europe/London\" 2024-01-02\n",
		"TZ=\"é🙂\" 2024-01-02\n", "TZ=\"bad\\q\" 2024-01-02\n",
	}
	for _, boundary := range []int{4095, 4096, 65535, 65536, 65537} {
		prefix := "2024-01-02 ("
		inputs = append(inputs, prefix+strings.Repeat("x", boundary-len(prefix))+"é🙂\xff)\n")
		inputs = append(inputs, strings.Repeat(" ", boundary)+"FOO\xffé🙂\n")
	}
	var cases []DateByteCase
	for i, input := range inputs {
		for _, file := range []bool{false, true} {
			for _, debug := range []bool{false, true} {
				cases = append(cases, DateByteCase{Name: fmt.Sprintf("input%d file%t debug%t", i, file, debug), Input: input, File: file, Debug: debug})
			}
		}
	}
	for i, zone := range []string{"\xff", "\xed\xa0\x80", "é\xe2\x82"} {
		for _, file := range []bool{false, true} {
			for _, debug := range []bool{false, true} {
				cases = append(cases, DateByteCase{Name: fmt.Sprintf("invalid-zone%d file%t debug%t", i, file, debug), Input: "TZ=\"" + zone + "\" 2024-01-02\n", File: file, Debug: debug, InvalidZone: true})
			}
		}
	}
	return cases
}

func RunDateByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "date"
	if runtime.GOOS == "darwin" {
		name = "gdate"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "date")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU date")
	}
	version, err := exec.Command(oracle, "--version").Output()
	firstLine := strings.SplitN(string(version), "\n", 2)[0]
	if err != nil || !strings.Contains(firstLine, "coreutils)") {
		t.Skip("requires GNU date")
	}
	fields := strings.Fields(firstLine)
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU version: %s", firstLine)
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	trailer := regexp.MustCompile(`leakcheck: allocs=\d+ frees=\d+ live_bytes=\d+\n$`)
	normalize := func(s string) string {
		s = trailer.ReplaceAllString(s, "")
		s = strings.ReplaceAll(s, oracle+": ", "date: ")
		s = strings.ReplaceAll(s, filepath.Base(oracle)+": ", "date: ")
		s = strings.ReplaceAll(s, bin+": ", "date: ")
		return strings.ReplaceAll(s, filepath.Base(bin)+": ", "date: ")
	}
	t.Logf("reference: %s", firstLine)
	for _, tc := range DateByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			path := "-"
			input := tc.Input
			if tc.File {
				path = "dates"
				if err := os.WriteFile(filepath.Join(dir, path), []byte(input), 0o644); err != nil {
					t.Fatal(err)
				}
				input = ""
			}
			args := []string{"-u", "-f", path, "+%s"}
			if tc.Debug {
				args = append(args, "--debug")
			}
			run := func(command []string) (string, string, int) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, command[0], append(command[1:], args...)...)
				cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C", "TZ=UTC0"), strings.NewReader(input)
				var out, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostic
				if err := cmd.Run(); err != nil {
					if _, ok := err.(*exec.ExitError); !ok {
						t.Fatalf("run: %v", err)
					}
				}
				return out.String(), diagnostic.String(), cmd.ProcessState.ExitCode()
			}
			out, diagnostic, code := run(append(append([]string{}, runner...), bin))
			if census != nil {
				census(t, diagnostic)
			}
			got := normalize(diagnostic)
			if tc.InvalidZone {
				// D10: a timezone crossing into text must be complete UTF-8.
				if code != 1 || out != "" || !strings.Contains(got, "invalid date") || tc.Debug && !strings.Contains(got, "invalid UTF-8 timezone") {
					t.Fatalf("invalid timezone: exit %d, stdout %q, stderr %q", code, out, got)
				}
				return
			}
			want, wantErr, wantCode := run([]string{oracle})
			if code != wantCode || out != want || got != normalize(wantErr) {
				t.Fatalf("got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", code, out, got, wantCode, want, normalize(wantErr))
			}
		})
	}
}
