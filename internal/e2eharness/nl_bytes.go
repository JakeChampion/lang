package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type NlByteCase struct {
	Name, Data string
	Args       []string
}

func NlByteCases() []NlByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("x\n")
	}
	cases := []NlByteCase{
		{Name: "all bytes", Data: all.String()},
		{Name: "unterminated", Data: "a\xff\nb\xc3\nc"},
		{Name: "all style", Data: "\xff\n\n\n\xc0\n", Args: []string{"-ba"}},
		{Name: "blank groups", Data: "\xff\n\n\n\n\xfe\n\n\n", Args: []string{"-ba", "-l2"}},
		{Name: "none style", Data: "\xff\n\x00\n\xc0", Args: []string{"-bn"}},
		{Name: "raw regex capture", Data: "\xff\xff\na\n\x00\x00\n", Args: []string{"-b", `p\(.\)\1`}},
		{Name: "partial regex scalar", Data: "\xffé\n\xfeê\n\x00\n", Args: []string{"-b", "pé*"}},
		{Name: "raw regex class", Data: "\xffAA\n\x00BB\n\xc3\n", Args: []string{"-b", "p[A-Z]"}},
		{Name: "unicode delimiters", Data: "ééé\nheader\xff\néé\nbody\x00\né\nfooter\xc3\n", Args: []string{"-d", "é", "-ha", "-fa"}},
		{Name: "default pages", Data: "\\:\\:\\:\nhead\xff\n\\:\\:\nbody\x00\n\\:\nfoot\xc0\n", Args: []string{"-ha", "-fa", "-p"}},
		{Name: "left format", Data: "\xff\n\xfe\n", Args: []string{"-nln", "-s", "é", "-v99"}},
		{Name: "zero format", Data: "\xff\n\xfe\n", Args: []string{"-nrz", "-v-1", "-i-1"}},
		{Name: "overflow", Data: "\xff\n\xfe\n", Args: []string{"-v9223372036854775807"}},
		{Name: "max final", Data: "\xff\n", Args: []string{"-v9223372036854775807"}},
		{Name: "empty", Data: ""},
		{Name: "long line", Data: strings.Repeat("\xffé\x00", 50000) + "\n\xc0\n"},
		{Name: "long unterminated", Data: strings.Repeat("\xffé\x00", 50000)},
	}
	for _, n := range []int{65535, 65536, 65537, 131071, 131072, 131073} {
		data := strings.Repeat("\xff", n) + "é\nraw\x00\xc0\n"
		cases = append(cases, NlByteCase{Name: fmt.Sprintf("read boundary %d", n), Data: data}, NlByteCase{Name: fmt.Sprintf("regex boundary %d", n), Data: data, Args: []string{"-b", "pé*"}})
	}
	return cases
}

func RunNlByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "nl"
	if runtime.GOOS == "darwin" {
		name = "gnl"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "nl")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU nl")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU nl")
	}
	for _, tc := range NlByteCases() {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.Name, file), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "input"), []byte(tc.Data), 0o644); err != nil {
					t.Fatal(err)
				}
				args := append([]string{}, tc.Args...)
				if file {
					args = append(args, "input")
				}

				run := func(prefix []string) (string, string, int) {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					argv := append(append([]string{}, prefix...), args...)
					cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
					cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(tc.Data)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					err := cmd.Run()
					if ctx.Err() != nil || cmd.ProcessState == nil || (err != nil && cmd.ProcessState.ExitCode() < 0) {
						t.Fatalf("run %s: %v\n%s", argv[0], err, diagnostic.String())
					}
					return out.String(), diagnostic.String(), cmd.ProcessState.ExitCode()
				}
				want, wantErr, wantCode := run([]string{oracle})
				got, diagnostic, code := run(append(append([]string{}, runner...), bin))
				if code != wantCode || got != want {
					t.Fatalf("exit/stdout differ: exit %d/%d, output bytes %d/%d\n%s", code, wantCode, len(got), len(want), diagnostic)
				}
				if census != nil && code == 0 {
					census(t, diagnostic)
				}
				var clean []string
				for _, line := range strings.SplitAfter(diagnostic, "\n") {
					if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
						if code != 0 {
							t.Log(strings.TrimSpace(line))
						}
						continue
					}
					clean = append(clean, line)
				}
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "nl:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "nl:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "nl:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
