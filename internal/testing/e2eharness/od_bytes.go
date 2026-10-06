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

type OdByteCase struct {
	Name, Data string
	Args       []string
	Parts      []string
}

func OdByteCases() []OdByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
	}
	raw := all.String()
	cases := []OdByteCase{
		{Name: "empty", Data: ""},
		{Name: "default", Data: raw},
		{Name: "all bytes hex", Data: raw, Args: []string{"-t", "x1z"}},
		{Name: "all bytes chars", Data: raw, Args: []string{"-t", "ac"}},
		{Name: "partial units", Data: raw[:251], Args: []string{"-t", "x8d4u2"}},
		{Name: "big endian", Data: raw[:253], Args: []string{"--endian=big", "-t", "x8d4u2"}},
		{Name: "little endian", Data: raw[:253], Args: []string{"--endian=little", "-t", "x8d4u2"}},
		{Name: "float units", Data: raw[:253], Args: []string{"-t", "f4f8"}},
		{Name: "skip limit", Data: raw, Args: []string{"-j", "127", "-N", "67", "-t", "x2"}},
		{Name: "skip past end", Data: raw, Args: []string{"-j", "257"}},
		{Name: "zero limit", Data: raw, Args: []string{"-N", "0"}},
		{Name: "fold raw blocks", Data: strings.Repeat("\xff\xc3\x00\xfe", 40000) + "\xff\xc3"},
		{Name: "keep raw blocks", Data: strings.Repeat("\xff\xc3\x00\xfe", 40), Args: []string{"-v"}},
		{Name: "no address", Data: raw, Args: []string{"-A", "n", "-t", "x1"}},
		{Name: "unaligned width", Data: raw, Args: []string{"-w7", "-t", "x1z"}},
		{Name: "large block", Data: strings.Repeat(raw, 520) + "\xff", Args: []string{"-w65537", "-t", "x1"}},
		{Name: "strings", Data: "hello\x00bad\xfftail\x00éplain\x00A\x00unterminated", Args: []string{"-S3"}},
		{Name: "strings boundary", Data: strings.Repeat("x", 70000) + "\x00bad\xffgood\x00", Args: []string{"-S4"}},
		{Name: "strings cut", Data: "hi\xffabcdefghijk", Args: []string{"-S4", "-N10"}},
		{Name: "unicode radix", Args: []string{"-A", "é"}},
		{Name: "empty radix", Args: []string{"-A", ""}},
		{Name: "unicode type", Args: []string{"-t", "é"}},
	}
	for _, n := range []int{65535, 65536, 65537, 131071, 131072, 131073} {
		cases = append(cases, OdByteCase{Name: fmt.Sprintf("boundary %d", n), Data: strings.Repeat("\xff", n) + "é\xc3\x00", Args: []string{"-t", "x4z"}})
	}
	parts := []string{"\xff", "", "é\x00", "\xfe\xc3", strings.Repeat("x\xff", 40000), "\x00tail"}
	for _, args := range [][]string{{"-t", "x4z"}, {"-j", "4", "-N", "65537", "-t", "x2"}, {"-S3"}} {
		cases = append(cases, OdByteCase{Name: "multiple files " + strings.Join(args, " "), Data: strings.Join(parts, ""), Parts: parts, Args: args})
	}
	return cases
}

func RunOdByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "od"
	if runtime.GOOS == "darwin" {
		name = "god"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "od")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU od")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU od")
	}
	for _, tc := range OdByteCases() {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.Name, file), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "input"), []byte(tc.Data), 0o644); err != nil {
					t.Fatal(err)
				}
				args := append([]string{}, tc.Args...)
				if file {
					if len(tc.Parts) == 0 {
						args = append(args, "input")
					} else {
						for i, part := range tc.Parts {
							name := fmt.Sprintf("part-%d", i)
							if err := os.WriteFile(filepath.Join(dir, name), []byte(part), 0o644); err != nil {
								t.Fatal(err)
							}
							args = append(args, name)
						}
					}
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
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "od:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "od:")
				wantErr = strings.ReplaceAll(wantErr, oracle+":", "od:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "od:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
