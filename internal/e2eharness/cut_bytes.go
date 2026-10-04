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

type CutByteCase struct {
	Name, Input string
	Args        []string
	Parts       []string
}

func CutByteCases() []CutByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	input := "é\xff\t\x80:x\n" + string(all) + "\n\xff\x00:é\t\x80"
	options := [][]string{
		{"-b1,3-9,256-"}, {"-c2,4-"}, {"-b1-4", "--complement"},
		{"-b1,3,5", "--output-delimiter=é"}, {"-b1,3,5", "--output-delimiter="},
		{"-z", "-b1-3"}, {"-f1,3"}, {"-f2,4", "-d:"},
		{"-sf2", "-d:"}, {"-f1,3", "-d", ""},
		{"-f1,3", "-d\n"}, {"-z", "-f1,3", "-d", ""},
	}
	var cases []CutByteCase
	for i, flags := range options {
		label := fmt.Sprintf("mode %d", i)
		cases = append(cases,
			CutByteCase{Name: label + " pipe", Input: input, Args: flags},
			CutByteCase{Name: label + " file", Parts: []string{input}, Args: flags},
			CutByteCase{Name: label + " empty", Args: flags},
			CutByteCase{Name: label + " file boundaries", Parts: []string{"\xff:é", "", "\x80\t\xff:\x00"}, Args: flags},
		)
	}
	for _, flags := range [][]string{{"-b1,65536,131073-"}, {"-f1,3", "-d:"}, {"-sf2", "-d:"}} {
		label := strings.Join(flags, " ")
		long := strings.Repeat("\xff", 65535) + ":" + strings.Repeat("\x80", 65536) + ":é"
		cases = append(cases,
			CutByteCase{Name: "long terminated " + label, Parts: []string{long + "\n"}, Args: flags},
			CutByteCase{Name: "long tail " + label, Parts: []string{long}, Args: flags},
			CutByteCase{Name: "after long tail " + label, Parts: []string{long, "x:\xff:y\n"}, Args: flags},
		)
	}
	return cases
}

func RunCutByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "cut"
	if runtime.GOOS == "darwin" {
		name = "gcut"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "cut")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU cut")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU cut")
	}
	fields := strings.Fields(strings.SplitN(string(version), "\n", 2)[0])
	if len(fields) == 0 {
		t.Fatal("missing GNU cut version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU cut version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range CutByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{}, tc.Args...)
			for i, part := range tc.Parts {
				name := fmt.Sprintf("part%d", i)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(part), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, name)
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
			}
		})
	}
}
