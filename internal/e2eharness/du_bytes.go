package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type DUByteCase struct {
	Name, Input, Names, Patterns string
	Args                         []string
	InvalidName                  bool
	Collation                    bool
}

func DUByteCases() []DUByteCase {
	var cases []DUByteCase
	for i, names := range []string{"", "f\x00ab\x00é🙂\x00empty\x00", "é🙂", "f\x00f\x00ab", strings.Repeat("f\x00", 32766) + "ab\x00é🙂\x00"} {
		cases = append(cases,
			DUByteCase{Name: fmt.Sprintf("names%d file", i), Names: names, Args: []string{"-b", "--files0-from=names"}},
			DUByteCase{Name: fmt.Sprintf("names%d stdin", i), Input: names, Args: []string{"-b", "--files0-from=-"}},
		)
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	patterns := []string{"", "f\nab\n", "é*\n", "*\xff*\n", "[\xff]\n", "[!\xff]*\n", "[[:alpha:]]*\n", "[[.\xff.]]\n", "[[:\xff:]]\n", "f\x00ignored\nab\r\n", "f\r\r\n", "[é]*\n", string(all), strings.Repeat("unmatched\n", 8191) + "é*\n", "[[.f.]]\n", "[[=f=]]\n", "[[:alpha]\n", "[[:unknown:]]\n", "[![:digit:]]*\n", "[a-f]*\n"}
	for i, pattern := range patterns {
		operands := []string{"f", "ab", "é🙂", "empty", "f]", "=]"}
		cases = append(cases,
			DUByteCase{Name: fmt.Sprintf("patterns%d file", i), Patterns: pattern, Args: append([]string{"-b", "-X", "patterns"}, operands...), Collation: i == 14 || i == 15},
			DUByteCase{Name: fmt.Sprintf("patterns%d stdin", i), Input: pattern, Args: append([]string{"-b", "-X", "-"}, operands...), Collation: i == 14 || i == 15},
		)
	}
	cases = append(cases, DUByteCase{Name: "shared stdin", Input: "f\nab\n", Args: []string{"-b", "-X", "-", "--files0-from=-"}})
	for i, names := range []string{"\xff", "f\x00\xed\xa0\x80\x00", "f\x00é\xe2\x82"} {
		cases = append(cases,
			DUByteCase{Name: fmt.Sprintf("invalid%d file", i), Names: names, Args: []string{"-b", "--files0-from=names"}, InvalidName: true},
			DUByteCase{Name: fmt.Sprintf("invalid%d stdin", i), Input: names, Args: []string{"-b", "--files0-from=-"}, InvalidName: true},
		)
	}
	return cases
}

func RunDUByteCases(t *testing.T, bin string, runner []string, target string, census func(*testing.T, string)) {
	t.Helper()
	name := "du"
	if runtime.GOOS == "darwin" {
		name = "gdu"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "du")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU du")
	}
	version, err := exec.Command(oracle, "--version").Output()
	firstLine := strings.SplitN(string(version), "\n", 2)[0]
	if err != nil || !(strings.HasPrefix(firstLine, "du (GNU coreutils) ") || strings.HasPrefix(firstLine, "du (coreutils) ")) {
		t.Skip("requires GNU du")
	}
	fields := strings.Fields(firstLine)
	if len(fields) == 0 {
		t.Fatal("missing GNU du version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU du version: %s", firstLine)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, firstLine)
	for _, tc := range DUByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"f": "abc", "ab": "12345", "é🙂": "xy", "empty": "", "f]": "fallback", "=]": "equivalence", "names": tc.Names, "patterns": tc.Patterns} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			env := append(os.Environ(), "LC_ALL=C")
			var want []byte
			if !tc.InvalidName {
				ref := exec.CommandContext(ctx, oracle, tc.Args...)
				ref.Dir, ref.Env, ref.Stdin = dir, env, strings.NewReader(tc.Input)
				var err error
				want, err = ref.Output()
				if err != nil {
					t.Fatalf("GNU run: %v", err)
				}
				if tc.Collation && runtime.GOOS == "darwin" && !strings.Contains(target, "darwin") {
					// GNU 9.12 on Linux matches f for both [[.f.]] and [[=f=]].
					// Darwin uses the gnulib bracket fallback instead. Keep the
					// cross-target cases, with the measured Linux expectation.
					want = []byte("5\tab\n2\té🙂\n0\tempty\n8\tf]\n11\t=]\n")
				}
			}
			argv := append(append(append([]string{}, runner...), bin), tc.Args...)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(tc.Input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			if tc.InvalidName {
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || out.Len() != 0 || !strings.Contains(diagnostic.String(), "invalid UTF-8 file name") {
					t.Fatalf("invalid filename: %v output=%q\n%s", err, out.String(), diagnostic.String())
				}
			} else if err != nil || !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("run: %v output=%q want=%q\n%s", err, out.Bytes(), want, diagnostic.String())
			}
			if census != nil {
				census(t, diagnostic.String())
			} else if !tc.InvalidName && diagnostic.Len() != 0 {
				t.Fatalf("unexpected diagnostic: %s", diagnostic.String())
			}
		})
	}
}
