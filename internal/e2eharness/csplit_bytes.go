package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type CsplitByteCase struct {
	Name, Data        string
	Options, Patterns []string
}

func CsplitByteCases() []CsplitByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("x\n")
	}
	raw := "a\xff\nb\xc3\nc\x00\nd\xfe\n"
	cases := []CsplitByteCase{
		{Name: "all bytes", Data: all.String(), Patterns: []string{"128"}},
		{Name: "raw lines", Data: raw, Patterns: []string{"2", "4"}},
		{Name: "raw regex", Data: raw, Patterns: []string{"/b/"}},
		{Name: "raw capture", Data: "a\xff\xff\nb\xfe\nc\x00\x00\n", Patterns: []string{`/\(.\)\1/`}},
		{Name: "partial scalar regex", Data: "a\xffé\nb\xfeê\nc\xc3\n", Patterns: []string{"/é*/"}},
		{Name: "backward offset", Data: raw, Patterns: []string{"/c/-1"}},
		{Name: "forward offset", Data: raw, Patterns: []string{"/b/+1"}},
		{Name: "ignored piece", Data: raw, Patterns: []string{"%b%"}},
		{Name: "suppressed line", Data: raw, Options: []string{"--suppress-matched"}, Patterns: []string{"/b/"}},
		{Name: "finite repetition", Data: strings.Repeat(raw, 3), Patterns: []string{"/b/", "{1}"}},
		{Name: "elide empty", Data: raw, Options: []string{"-z"}, Patterns: []string{"/a/"}},
		{Name: "unicode format", Data: raw, Options: []string{"-b", "é%%€%03d🙂"}, Patterns: []string{"2"}},
		{Name: "unicode prefix", Data: raw, Options: []string{"-f", "é"}, Patterns: []string{"2"}},
		{Name: "unterminated", Data: "a\xff\nb\xc3\nc\x00", Patterns: []string{"2"}},
		{Name: "missing match cleanup", Data: raw, Patterns: []string{"2", "/absent/"}},
		{Name: "missing match retained", Data: raw, Options: []string{"-k"}, Patterns: []string{"2", "/absent/"}},
		{Name: "empty", Data: "", Patterns: []string{"1"}},
		{Name: "long line", Data: strings.Repeat("\xffé\x00", 50000) + "\nb\xc3\nc\xfe\n", Patterns: []string{"2"}},
		{Name: "long regex line", Data: strings.Repeat("\xffé\x00", 50000) + "\nb\xc3\nc\xfe\n", Patterns: []string{"/b/"}},
		{Name: "streaming tail", Data: "a\xff\n" + strings.Repeat("\xffé\x00", 50000), Patterns: []string{"2"}},
	}
	for _, n := range []int{65535, 65536, 65537, 131071, 131072, 131073} {
		data := strings.Repeat("\xff", n) + "é\nmatch\x00\xc0\nlast\xfe\n"
		cases = append(cases, CsplitByteCase{Name: fmt.Sprintf("boundary %d", n), Data: data, Patterns: []string{"/match/"}})
	}
	return cases
}

func RunCsplitByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "csplit"
	if runtime.GOOS == "darwin" {
		name = "gcsplit"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "csplit")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU csplit")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU csplit")
	}
	for _, tc := range CsplitByteCases() {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.Name, file), func(t *testing.T) {
				type result struct {
					out, diagnostic string
					code            int
					files           map[string]string
				}
				run := func(prefix []string) result {
					dir := t.TempDir()
					input := "-"
					if file {
						input = "input"
						if err := os.WriteFile(filepath.Join(dir, input), []byte(tc.Data), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					argv := append(append([]string{}, prefix...), tc.Options...)
					argv = append(argv, input)
					argv = append(argv, tc.Patterns...)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
					cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(tc.Data)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					err := cmd.Run()
					if ctx.Err() != nil || cmd.ProcessState == nil || (err != nil && cmd.ProcessState.ExitCode() < 0) {
						t.Fatalf("run %s: %v\n%s", argv[0], err, diagnostic.String())
					}
					files := map[string]string{}
					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if entry.Name() == "input" {
							continue
						}
						data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
						if err != nil {
							t.Fatal(err)
						}
						files[entry.Name()] = string(data)
					}
					return result{out.String(), diagnostic.String(), cmd.ProcessState.ExitCode(), files}
				}
				want := run([]string{oracle})
				got := run(append(append([]string{}, runner...), bin))
				if got.code != want.code || got.out != want.out || !maps.Equal(got.files, want.files) {
					t.Fatalf("exit, counts or output files differ: exit %d/%d, counts %q/%q, files %d/%d\n%s", got.code, want.code, got.out, want.out, len(got.files), len(want.files), got.diagnostic)
				}
				if census != nil && got.code == 0 {
					census(t, got.diagnostic)
				}
				var clean []string
				for _, line := range strings.SplitAfter(got.diagnostic, "\n") {
					if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
						if got.code != 0 {
							t.Log(strings.TrimSpace(line))
						}
						continue
					}
					clean = append(clean, line)
				}
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "csplit:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "csplit:")
				wantErr := strings.ReplaceAll(want.diagnostic, oracle+":", "csplit:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "csplit:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
