package e2eharness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type WcByteCase struct {
	Name, Input string
	Args        []string
	Env         []string
	File        bool
	ListFile    bool
	Files       map[string]string
	Error       string
}

// WcByteCases exercises C-locale counting across read boundaries and the
// separate UTF-8 boundary at which a name becomes a filesystem path.
func WcByteCases() []WcByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	raw := strings.Repeat(string(all), 513) + "\xff\xc0\x80tail"
	cases := []WcByteCase{
		{Name: "all bytes all counts", Input: raw, Args: []string{"-clmwL"}},
		{Name: "raw lines", Input: raw, Args: []string{"-l"}},
		{Name: "raw words", Input: raw, Args: []string{"-w"}},
		{Name: "raw widths", Input: raw, Args: []string{"-L"}},
		{Name: "raw chars", Input: raw, Args: []string{"-m"}},
		{Name: "raw bytes", Input: raw, Args: []string{"-c"}},
		{Name: "word crossing boundary", Input: strings.Repeat("\xff", 65537) + " \x80", Args: []string{"-wL"}},
		{Name: "whitespace crossing boundary", Input: "x" + strings.Repeat(" ", 65536) + "\xff", Args: []string{"-wL"}},
		{Name: "newline at boundary", Input: strings.Repeat("x", 65535) + "\n\xff\tlast", Args: []string{"-clmwL"}},
		{Name: "tab at boundary", Input: strings.Repeat("x", 65535) + "\t\xffx\r\tlast\fend", Args: []string{"-clmwL"}},
		{Name: "raw final word", Input: "\xff\xc0\x80", Args: []string{"-clmwL"}},
		{Name: "empty all counts", Args: []string{"-clmwL"}},
		{Name: "no-break space", Input: "a\xa0b", Args: []string{"-w"}},
		{Name: "POSIX no-break space", Input: "a\xa0b", Args: []string{"-w"}, Env: []string{"POSIXLY_CORRECT=1"}},
		{Name: "empty POSIX flag", Input: "a\xa0b", Args: []string{"-w"}, Env: []string{"POSIXLY_CORRECT="}},
		{Name: "eight-byte POSIX flag", Input: "a\xa0b", Args: []string{"-w"}, Env: []string{"POSIXLY_CORRECT=12345678"}},
		{Name: "sixteen-byte POSIX flag", Input: "a\xa0b", Args: []string{"-w"}, Env: []string{"POSIXLY_CORRECT=1234567890123456"}},
	}
	stream := append([]WcByteCase(nil), cases...)
	for _, tc := range stream {
		tc.Name += " file"
		tc.File = true
		cases = append(cases, tc)
	}
	cases = append(cases,
		WcByteCase{Name: "filename scalar across reads", Input: strings.Repeat("f\x00", 2047) + "fé\x00", Args: []string{"-c", "--total=only", "--files0-from=-"}, Files: map[string]string{"f": "x", "fé": "xx"}},
		WcByteCase{Name: "malformed filename", Input: "f\x00bad\xff\x00", Args: []string{"--files0-from=-"}, Files: map[string]string{"f": "x"}, Error: "-:2: invalid UTF-8 file name"},
		WcByteCase{Name: "truncated filename scalar", Input: "bad\xe2\x82", Args: []string{"--files0-from=-"}, Error: "-:1: invalid UTF-8 file name"},
	)
	for _, tc := range append([]WcByteCase(nil), cases...) {
		if !strings.Contains(strings.Join(tc.Args, " "), "--files0-from=-") {
			continue
		}
		tc.Name += " list file"
		tc.ListFile = true
		tc.Error = strings.Replace(tc.Error, "-:", "names:", 1)
		cases = append(cases, tc)
	}
	return cases
}

func RunWcByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "wc"
	if runtime.GOOS == "darwin" {
		name = "gwc"
	}
	oracle, err := exec.LookPath(name)
	dirs := append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin")
	for _, dir := range dirs {
		candidate := filepath.Join(dir, "wc")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU wc")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU wc")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range WcByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.Files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := append([]string{}, tc.Args...)
			input := tc.Input
			if tc.ListFile {
				if err := os.WriteFile(filepath.Join(dir, "names"), []byte(input), 0o644); err != nil {
					t.Fatal(err)
				}
				for i, arg := range args {
					if arg == "--files0-from=-" {
						args[i] = "--files0-from=names"
					}
				}
				input = ""
			}
			if tc.File {
				if err := os.WriteFile(filepath.Join(dir, "input"), []byte(input), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "input")
				input = ""
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			env := []string{}
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "POSIXLY_CORRECT=") {
					env = append(env, entry)
				}
			}
			env = append(append(env, "LC_ALL=C"), tc.Env...)
			var want []byte
			if tc.Error == "" {
				ref := exec.CommandContext(ctx, oracle, args...)
				ref.Dir, ref.Env, ref.Stdin = dir, env, strings.NewReader(input)
				var err error
				want, err = ref.Output()
				if err != nil {
					t.Fatalf("GNU run: %v", err)
				}
			}
			prefix := append([]string{}, runner...)
			if len(prefix) > 0 && filepath.Base(prefix[0]) == "wasmtime" {
				for _, entry := range tc.Env {
					prefix = append(prefix, "--env", entry)
				}
			}
			argv := append(append(prefix, bin), args...)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			err := cmd.Run()
			if tc.Error != "" {
				if err == nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(diagnostic.String(), tc.Error) {
					t.Fatalf("run: %v, want exit 1 and %q\n%s", err, tc.Error, diagnostic.String())
				}
			} else if err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.String())
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("stdout = %q, want %q", out.Bytes(), want)
			}
			if census != nil {
				census(t, diagnostic.String())
			}
		})
	}
}
