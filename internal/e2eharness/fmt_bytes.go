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

type FmtByteCase struct {
	Name, Data string
	Args       []string
}

func FmtByteCases() []FmtByteCase {
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("x ")
	}
	raw := "alpha\xff beta\xc3 gamma\x00 delta\xfe epsilon\nsecond\xff line\x00 words\n"
	cases := []FmtByteCase{
		{Name: "all bytes", Data: all.String(), Args: []string{"-w", "35"}},
		{Name: "empty", Data: ""},
		{Name: "unterminated", Data: raw[:len(raw)-1]},
		{Name: "raw paragraph", Data: raw, Args: []string{"-w", "24"}},
		{Name: "split only", Data: raw, Args: []string{"-s", "-w", "24"}},
		{Name: "uniform spacing", Data: "a\xff.  b\xc3!   c\x00?    d\xfe\n", Args: []string{"-u", "-w", "12"}},
		{Name: "crown", Data: "  a\xff b\xfe c\x00 d\xc3\n    e\xff f\xfe g\x00 h\xc3\n", Args: []string{"-c", "-w", "14"}},
		{Name: "tagged", Data: "a\xff b\xfe c\x00 d\xc3\n  e\xff f\xfe g\x00 h\xc3\n", Args: []string{"-t", "-w", "14"}},
		{Name: "tabs", Data: "\ta\xff b\xfe c\x00 d\xc3\n\te\xff\tf\xfe g\x00 h\xc3\n", Args: []string{"-w", "24"}},
		{Name: "prefix", Data: "é a\xff b\xfe c\x00 d\xc3\né e\xff f\xfe g\x00 h\xc3\n", Args: []string{"-p", "é ", "-w", "14"}},
		{Name: "prefix passthrough", Data: raw, Args: []string{"-p", "é ", "-w", "14"}},
		{Name: "blank prefix", Data: "  a\xff b\xfe\n \x00x\n  \n", Args: []string{"-p", "  ", "-w", "12"}},
		{Name: "control bytes", Data: "a\xff\vb\xfe\fc\x00\rd\xc3\bE\n"},
		{Name: "long word", Data: strings.Repeat("\xffé\x00", 40000) + "\nlast\xc3\n"},
		{Name: "long paragraph", Data: strings.Repeat("a\xff b\xfe c\x00 d\xc3 ", 600) + "\n", Args: []string{"-w", "28"}},
		{Name: "carry cleared", Data: strings.Repeat("\xff", 70000) + "\n\nA\xc3\n\nB\xfe\n"},
	}
	for _, n := range []int{65535, 65536, 65537, 131071, 131072, 131073} {
		cases = append(cases, FmtByteCase{Name: fmt.Sprintf("boundary %d", n), Data: strings.Repeat("\xff", n) + "é\nalpha\xc3 beta\x00\n"})
	}
	return cases
}

func RunFmtByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "fmt"
	if runtime.GOOS == "darwin" {
		name = "gfmt"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "fmt")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU fmt")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU fmt")
	}
	for _, tc := range FmtByteCases() {
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
				actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "fmt:")
				actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "fmt:")
				wantErr = strings.ReplaceAll(wantErr, oracle+":", "fmt:")
				wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "fmt:")
				if actualErr != wantErr {
					t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
				}
			})
		}
	}
}
