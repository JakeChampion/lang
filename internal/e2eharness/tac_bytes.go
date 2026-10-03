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

type TacByteCase struct {
	Name, Data string
	Args       []string
}

func TacByteCases() []TacByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("x\n")
	}
	cases := []TacByteCase{
		{Name: "all bytes", Data: all.String()},
		{Name: "unterminated", Data: "a\xff\nb\xc3\nc"},
		{Name: "before", Data: "a\x00\nb\xff\n", Args: []string{"-b"}},
		{Name: "NUL separator", Data: "a\x00b\xff\x00c", Args: []string{"-s", ""}},
		{Name: "unicode separator", Data: "a\xfféb\xc3éc", Args: []string{"-s", "é"}},
		{Name: "partial regex scalar", Data: "\xffé\xfeêc", Args: []string{"-r", "-s", "é*"}},
		{Name: "raw regex capture", Data: "a\xff\xffb\x00\x00c", Args: []string{"-r", "-s", `\(.\)\1`}},
		{Name: "regex before", Data: "a\xfféb\xfeêc", Args: []string{"-b", "-r", "-s", "é*"}},
		{Name: "regex class", Data: "\xffAA\x00BB\xc3CC", Args: []string{"-r", "-s", "[A-Z][A-Z]*"}},
		{Name: "empty", Data: ""},
		{Name: "long records", Data: strings.Repeat("\xffé\x00", 40000) + "\n" + strings.Repeat("\xc3", 20000)},
	}
	for _, n := range []int{8191, 8192, 8193, 16383, 16384, 16385} {
		data := strings.Repeat("\xff", n) + "é\nraw\x00\xc0\n"
		cases = append(cases, TacByteCase{Name: fmt.Sprintf("block boundary %d", n), Data: data}, TacByteCase{Name: fmt.Sprintf("regex boundary %d", n), Data: data, Args: []string{"-r", "-s", "é*"}})
	}
	return cases
}

func RunTacByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "tac"
	if runtime.GOOS == "darwin" {
		name = "gtac"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "tac")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU tac")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU tac")
	}
	for _, tc := range TacByteCases() {
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
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "tac:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "tac:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "tac:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
