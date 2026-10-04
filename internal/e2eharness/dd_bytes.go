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

type DDByteCase struct {
	Name, Input string
	Args        []string
	Parts       []string
}

func DDByteCases() []DDByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	inputs := []string{"", string(all), "é🙂\xff\x00\x80\xc0\xaf\xed\xa0\x80", "\xff a\n\x00\x80 long record\n\nlast"}
	options := [][]string{
		{"bs=257"}, {"ibs=3", "obs=4"}, {"ibs=7", "obs=11", "iflag=fullblock"},
		{"ibs=3", "obs=4", "conv=swab"}, {"ibs=7", "obs=11", "conv=sync"},
		{"ibs=3", "obs=7", "conv=swab,sync"},
		{"ibs=7", "obs=11", "conv=ucase"}, {"ibs=7", "obs=11", "conv=lcase"},
		{"ibs=7", "obs=11", "conv=ascii"}, {"ibs=7", "obs=11", "conv=ebcdic"},
		{"ibs=7", "obs=11", "conv=ibm"},
		{"ibs=3", "obs=7", "cbs=5", "conv=block"},
		{"ibs=3", "obs=7", "cbs=5", "conv=unblock"},
		{"ibs=3", "obs=7", "cbs=5", "conv=ebcdic,block"},
		{"ibs=3", "obs=7", "cbs=5", "conv=ascii,unblock"},
	}
	var cases []DDByteCase
	for i, input := range inputs {
		for j, args := range options {
			cases = append(cases,
				DDByteCase{Name: fmt.Sprintf("input%d mode%d pipe", i, j), Input: input, Args: args},
				DDByteCase{Name: fmt.Sprintf("input%d mode%d file", i, j), Parts: []string{input}, Args: args},
			)
		}
	}
	for _, size := range []int{4095, 4096, 4097, 65535, 65536, 65537} {
		input := strings.Repeat(string(all), (size+255)/256)[:size]
		for _, conv := range []string{"swab", "sync", "ucase"} {
			cases = append(cases, DDByteCase{
				Name: fmt.Sprintf("boundary%d %s", size, conv), Parts: []string{input},
				Args: []string{"ibs=65536", "obs=4097", "iflag=fullblock", "conv=" + conv},
			})
		}
	}
	return cases
}

func RunDDByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "dd"
	if runtime.GOOS == "darwin" {
		name = "gdd"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "dd")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU dd")
	}
	version, err := exec.Command(oracle, "--version").Output()
	firstLine := strings.SplitN(string(version), "\n", 2)[0]
	if err != nil || !(strings.HasPrefix(firstLine, "dd (GNU coreutils) ") || strings.HasPrefix(firstLine, "dd (coreutils) ")) {
		t.Skip("requires GNU dd")
	}
	fields := strings.Fields(firstLine)
	if len(fields) == 0 {
		t.Fatal("missing GNU dd version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU dd version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, firstLine)
	for _, tc := range DDByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{"status=none"}, tc.Args...)
			for i, part := range tc.Parts {
				name := fmt.Sprintf("part%d", i)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(part), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "if="+name)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			env := append(os.Environ(), "LC_ALL=C")
			ref := exec.CommandContext(ctx, oracle, args...)
			ref.Dir, ref.Env, ref.Stdin = dir, env, strings.NewReader(tc.Input)
			want, err := ref.Output()
			if err != nil {
				t.Fatalf("GNU run: %v", err)
			}
			argv := append(append(append([]string{}, runner...), bin), args...)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(tc.Input)
			var out, diagnostic bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &diagnostic
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s", err, diagnostic.String())
			}
			if !bytes.Equal(out.Bytes(), want) {
				t.Fatalf("binary output differs: got %d bytes, want %d", out.Len(), len(want))
			}
			if census != nil {
				census(t, diagnostic.String())
			} else if diagnostic.Len() != 0 {
				t.Fatalf("unexpected diagnostic: %s", diagnostic.String())
			}
		})
	}
}
