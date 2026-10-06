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

type SortByteCase struct {
	Name, Input string
	Args        []string
	Files       map[string]string
	InvalidName bool
}

func SortByteCases() []SortByteCase {
	var cases []SortByteCase
	inputs := []string{
		"",
		"é🙂\n\xff\n\x00\n\xed\xa0\x80\n\xc0\xaf\n\xe2\x82\n\x80\nA\na\n",
		"10\xff\n2\x80\n-3\xed\xa0\x80\n0x1p2\xff\nnan\xff\ninf\x80\n.5\xff\n1K\xff\nJan\xff\nDec\x80\n",
		"p v2.é\np v10.\xff\np .v2\np ..\np v1~\xff\np v1\x80\np v1\x80\n",
	}
	modes := [][]string{nil, {"-r"}, {"-u"}, {"-s"}, {"-n"}, {"-g"}, {"-h"}, {"-M"}, {"-V"}, {"-f"}, {"-d"}, {"-i"}, {"-k2,2V"}, {"-k1.2,1.3"}, {"--debug", "-g"}, {"-z"}}
	for i, input := range inputs {
		for j, mode := range modes {
			cases = append(cases,
				SortByteCase{Name: fmt.Sprintf("input%d mode%d stdin", i, j), Input: input, Args: mode},
				SortByteCase{Name: fmt.Sprintf("input%d mode%d file", i, j), Files: map[string]string{"input": input}, Args: append(append([]string{}, mode...), "input")},
			)
		}
	}
	for _, n := range []int{4095, 4096, 65535, 65536, 262143, 262144, 262145} {
		wide := strings.Repeat("x", n) + "é🙂\xff\n"
		cases = append(cases,
			SortByteCase{Name: fmt.Sprintf("boundary%d sort", n), Input: wide + "a\x80\n" + wide},
			SortByteCase{Name: fmt.Sprintf("boundary%d ordered", n), Input: "a\x80\n" + wide, Args: []string{"-c"}},
			SortByteCase{Name: fmt.Sprintf("boundary%d disorder", n), Input: wide + "a\xff\n", Args: []string{"-c"}},
			SortByteCase{Name: fmt.Sprintf("boundary%d merge", n), Files: map[string]string{"left": "a\x80\n" + wide, "right": "b\xff\n" + wide}, Args: []string{"-m", "-u", "left", "right"}},
		)
	}
	for i, seed := range []string{strings.Repeat("\x00", 16), strings.Repeat("\xff\x80\xc0\xaf", 4), "é🙂\xed\xa0\x80\xffabcdef"} {
		cases = append(cases, SortByteCase{Name: fmt.Sprintf("random%d", i), Input: inputs[1] + inputs[2], Files: map[string]string{"seed": seed}, Args: []string{"-R", "--random-source=seed"}})
	}
	for _, mode := range [][]string{{"-c"}, {"-C"}, {"-cu"}, {"-z", "-c"}, {"-V", "-c"}} {
		cases = append(cases, SortByteCase{Name: "check " + strings.Join(mode, " "), Input: "z\xff\na\x80\nz\x00a\n", Args: mode})
	}
	cases = append(cases,
		SortByteCase{Name: "merge no newline", Files: map[string]string{"left": "a\xff\nc\x80", "right": "b\xed\xa0\x80\nd\xff"}, Args: []string{"-m", "left", "right"}},
		SortByteCase{Name: "file names Unicode", Files: map[string]string{"names": "é🙂\x00f\x00", "é🙂": "z\xff\n", "f": "a\x80\n"}, Args: []string{"--files0-from=names"}},
		SortByteCase{Name: "file names Unicode stdin", Input: "é🙂\x00f\x00", Files: map[string]string{"é🙂": "z\xff\n", "f": "a\x80\n"}, Args: []string{"--files0-from=-"}},
	)
	for i, name := range []string{"\xff", "\xed\xa0\x80", "é\xe2\x82"} {
		cases = append(cases,
			SortByteCase{Name: fmt.Sprintf("invalid%d file", i), Files: map[string]string{"names": name}, Args: []string{"--files0-from=names"}, InvalidName: true},
			SortByteCase{Name: fmt.Sprintf("invalid%d stdin", i), Input: name, Args: []string{"--files0-from=-"}, InvalidName: true},
		)
	}
	return cases
}

func RunSortByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "sort"
	if runtime.GOOS == "darwin" {
		name = "gsort"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "sort")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU sort")
	}
	version, err := exec.Command(oracle, "--version").Output()
	first := strings.SplitN(string(version), "\n", 2)[0]
	if err != nil || !(strings.HasPrefix(first, "sort (GNU coreutils) ") || strings.HasPrefix(first, "sort (coreutils) ")) {
		t.Skip("requires GNU sort")
	}
	fields := strings.Fields(first)
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized version: %s", first)
	}
	major, e1 := strconv.Atoi(parts[0])
	minor, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s", first)
	censusTrailer := regexp.MustCompile(`leakcheck: allocs=[0-9]+ frees=[0-9]+ live_bytes=[0-9]+\n$`)
	for _, tc := range SortByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.Files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run := func(command []string) ([]byte, string, int) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				argv := append(append([]string{}, command...), tc.Args...)
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Dir, cmd.Env, cmd.Stdin = dir, ChildEnv("LC_ALL=C"), strings.NewReader(tc.Input)
				var out, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostic
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil {
					t.Fatalf("run: %v\n%s", err, diagnostic.String())
				}
				return out.Bytes(), diagnostic.String(), cmd.ProcessState.ExitCode()
			}
			normalize := func(diagnostic string) string {
				// A zero-terminated disorder line has no trailing newline;
				// the allocation trailer can follow its NUL directly.
				if census != nil {
					diagnostic = censusTrailer.ReplaceAllString(diagnostic, "")
				}
				var out strings.Builder
				for _, line := range strings.SplitAfter(diagnostic, "\n") {
					if strings.HasPrefix(line, "leakcheck:") {
						continue
					}
					for _, program := range []string{bin, filepath.Base(bin), oracle, filepath.Base(oracle), "sort", "gsort"} {
						if strings.HasPrefix(line, program+": ") {
							line = strings.TrimPrefix(line, program+": ")
							break
						}
					}
					out.WriteString(line)
				}
				return out.String()
			}
			var want []byte
			var diagnostic string
			var code int
			if tc.InvalidName {
				code = 2
				label := "names"
				if tc.Input != "" {
					label = "-"
				}
				diagnostic = label + ":1: invalid UTF-8 file name\n"
			} else {
				want, diagnostic, code = run([]string{oracle})
				diagnostic = normalize(diagnostic)
			}
			out, stderr, gotCode := run(append(append([]string{}, runner...), bin))
			if census != nil {
				// The census reader expects a line boundary, but -z diagnostics
				// end in NUL. Keep the original bytes for the GNU comparison.
				census(t, censusTrailer.ReplaceAllString(stderr, "\n$0"))
			}
			if gotCode != code || !bytes.Equal(out, want) || normalize(stderr) != diagnostic {
				t.Fatalf("exit=%d want=%d; output=%q want=%q; diagnostic=%q want=%q", gotCode, code, out, want, normalize(stderr), diagnostic)
			}
		})
	}
}
