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

type JoinByteCase struct {
	Name, A, B, Stdin string
	Args              []string
}

func JoinByteCases() []JoinByteCase {
	var keys strings.Builder
	for b := 0; b < 256; b++ {
		if b != '\n' && b != ':' {
			keys.WriteByte(byte(b))
			keys.WriteString(":\xff\xc0\x80\n")
		}
	}
	long := strings.Repeat("\xffé\x00", 50000)
	cases := []JoinByteCase{
		{Name: "all byte keys", A: keys.String(), B: keys.String(), Args: []string{"-t:", "a", "b"}},
		{Name: "colon key", A: ":,\xff\n", B: ":,\x80\n", Args: []string{"-t,", "a", "b"}},
		{Name: "newline key", A: "\n:\xff\x00", B: "\n:\x80\x00", Args: []string{"-z", "-t:", "a", "b"}},
		{Name: "raw duplicates", A: "\xff a\n\xff b\n", B: "\xff c\n\xff d\n", Args: []string{"a", "b"}},
		{Name: "raw case fold", A: "A\xff left\n", B: "a\xff right\n", Args: []string{"-i", "a", "b"}},
		{Name: "raw unpaired fields", A: "a \xff\nb \xc0\n", B: "a \x80\nc \xfe\n", Args: []string{"-a1", "-a2", "-o", "0,1.2,2.2", "-e", "é", "a", "b"}},
		{Name: "raw headers", A: "key \xff\na \x80\n", B: "key \xfe\na \xc0\n", Args: []string{"--header", "-o", "auto", "a", "b"}},
		{Name: "raw strict disorder", A: "b \xff\na \x80\n", B: "b \xfe\nz x\n", Args: []string{"--check-order", "a", "b"}},
		{Name: "raw deferred disorder", A: "a \xff\nz \x80\nc \xfe\nb x\n", B: "a y\nq z\n", Args: []string{"a", "b"}},
		{Name: "raw stdin", Stdin: "\xff x\n", B: "\xff y\n", Args: []string{"-", "b"}},
		{Name: "multi block fields", A: "a " + long + "\nb \xff", B: "a \x80\nb " + long, Args: []string{"a", "b"}},
		{Name: "multi block keys", A: long + " x\n", B: long + " y\n", Args: []string{"a", "b"}},
		{Name: "empty EOF", Args: []string{"a", "b"}},
		{Name: "unterminated final record", A: "\xff a", B: "\xff b", Args: []string{"a", "b"}},
	}
	for _, n := range []int{65535, 65536, 65537, 131072} {
		prefix := "a " + strings.Repeat("x", n-2)
		cases = append(cases, JoinByteCase{Name: fmt.Sprintf("record boundary %d", n),
			A: prefix + "\né \xff\n", B: "a \x80\né \xc0\n", Args: []string{"a", "b"}})
	}
	return cases
}

func RunJoinByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "join"
	if runtime.GOOS == "darwin" {
		name = "gjoin"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "join")
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU join")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU join")
	}
	for _, tc := range JoinByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"a": tc.A, "b": tc.B} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run := func(prefix []string) (string, string, int) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				argv := append(append([]string{}, prefix...), tc.Args...)
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(tc.Stdin)
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
			actualErr := strings.ReplaceAll(strings.Join(clean, ""), bin+":", "join:")
			actualErr = strings.ReplaceAll(actualErr, filepath.Base(bin)+":", "join:")
			wantErr = strings.ReplaceAll(wantErr, filepath.Base(oracle)+":", "join:")
			if actualErr != wantErr {
				t.Fatalf("stderr differs:\ngot  %q\nwant %q", actualErr, wantErr)
			}
		})
	}
}
