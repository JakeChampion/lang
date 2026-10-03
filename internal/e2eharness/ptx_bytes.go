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

type PtxByteCase struct {
	Name, Data string
	Args       []string
	Files      map[string]string
	Extra      []string
	Output     bool
}

func PtxByteCases() []PtxByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("x ")
	}
	raw := "z\xff alpha\xc3 beta\x00 gamma\xfe\n"
	cases := []PtxByteCase{
		{Name: "all bytes", Data: all.String()},
		{Name: "traditional all bytes", Data: all.String(), Args: []string{"-G"}},
		{Name: "empty", Data: ""},
		{Name: "unterminated", Data: raw[:len(raw)-1]},
		{Name: "raw words", Data: raw, Args: []string{"-W", `[^ \n]+`}},
		{Name: "raw escaped pattern", Data: raw, Args: []string{"-W", `\xff+`}},
		{Name: "raw sentence separator", Data: "alpha\xffbeta\xffgamma", Args: []string{"-S", `\xff`}},
		{Name: "raw capture", Data: "a\xff\xff b\xfe\xfe c\x00\x00", Args: []string{"-W", `\(.\)\1`}},
		{Name: "raw flag", Data: raw, Args: []string{"-w", "12", "-F", `\xff\xfe`}},
		{Name: "NUL flag", Data: raw, Args: []string{"-w", "12", "-F", `x\0y`}},
		{Name: "invalid pattern diagnostic", Data: raw, Args: []string{"-W", `[\xff`}},
		{Name: "empty sentence diagnostic", Data: raw, Args: []string{"-S", `\xff*`}},
		{Name: "break bytes", Data: raw, Args: []string{"-b", "breaks"}, Files: map[string]string{"breaks": " \xff\xc3\x00\xfe\n"}},
		{Name: "ignore bytes", Data: raw, Args: []string{"-G", "-i", "words"}, Files: map[string]string{"words": "z\xff\nbeta\x00\n"}},
		{Name: "only bytes folded", Data: raw, Args: []string{"-G", "-f", "-o", "words"}, Files: map[string]string{"words": "ALPHA\xc3\nGAMMA\xfe\n"}},
		{Name: "raw references", Data: "\xff\x00 Alpha beta\n\xc3 gamma delta\n", Args: []string{"-r"}},
		{Name: "raw references right", Data: "\xff\x00 Alpha beta\n\xc3 gamma delta\n", Args: []string{"-r", "-R"}},
		{Name: "multiple files", Data: raw, Args: []string{"-A", "-f"}, Files: map[string]string{"é-input": "\xfe Zebra\n\xc3 apple\n"}, Extra: []string{"é-input"}},
		{Name: "repeated stdin", Data: raw, Extra: []string{"-", "-"}},
		{Name: "traditional output", Data: raw, Args: []string{"-G"}, Output: true},
		{Name: "radix byte prefixes", Data: strings.Repeat("\xffabcdefgZ \xffabcdefgA \x80a \x00a ", 4097), Args: []string{"-G", "-f"}},
	}
	for _, format := range []string{"-O", "-T"} {
		cases = append(cases, PtxByteCase{Name: format + " raw escaping", Data: "\xff\"a$%&#{b}\\c_ \xc3z\n", Args: []string{format, "-G", "-F", `\xff`}}, PtxByteCase{Name: format + " raw references", Data: "\xff\"$ Alpha beta\n\xc3 gamma delta\n", Args: []string{format, "-r"}})
	}
	for _, width := range []string{"1", "2", "5", "9", "16", "32"} {
		cases = append(cases, PtxByteCase{Name: "layout " + width, Data: raw, Args: []string{"-w", width, "-g", "2", "-F", `\xffMARK`}})
	}
	for _, n := range []int{262143, 262144, 262145} {
		cases = append(cases, PtxByteCase{Name: fmt.Sprintf("read boundary %d", n), Data: strings.Repeat("\xff", n) + "é alpha\xc3 beta\x00\n"})
	}
	return cases
}

func RunPtxByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "ptx"
	if runtime.GOOS == "darwin" {
		name = "gptx"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "ptx")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU ptx")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU ptx")
	}
	for _, tc := range PtxByteCases() {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.Name, file), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "input"), []byte(tc.Data), 0o644); err != nil {
					t.Fatal(err)
				}
				for name, data := range tc.Files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				args := append([]string{}, tc.Args...)
				if file {
					args = append(args, "input")
				} else {
					args = append(args, "-")
				}
				args = append(args, tc.Extra...)
				if tc.Output {
					args = append(args, "output")
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
					if tc.Output && cmd.ProcessState.ExitCode() == 0 {
						data, err := os.ReadFile(filepath.Join(dir, "output"))
						if err != nil {
							t.Fatal(err)
						}
						out.Write(data)
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
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "ptx:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "ptx:")
				wantErr = strings.ReplaceAll(wantErr, oracle+":", "ptx:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "ptx:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
