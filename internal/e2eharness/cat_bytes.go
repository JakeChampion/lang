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

type CatByteCase struct {
	Name, Input string
	Args        []string
	Parts       []string
}

func CatByteCases() []CatByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	input := "\n\n" + string(all) + "\r\n\xff\x00\n\n\x80\xc0\xaf"
	var cases []CatByteCase
	for mask := range 64 {
		var args []string
		for i, flag := range []string{"-n", "-b", "-s", "-E", "-T", "-v"} {
			if mask&(1<<i) != 0 {
				args = append(args, flag)
			}
		}
		cases = append(cases,
			CatByteCase{Name: fmt.Sprintf("flags %02x pipe", mask), Input: input, Args: args},
			CatByteCase{Name: fmt.Sprintf("flags %02x file", mask), Parts: []string{input}, Args: args},
		)
	}
	for _, flags := range [][]string{{}, {"-E"}, {"-nE"}, {"-sE"}, {"-A"}, {"-nsA"}} {
		label := strings.Join(flags, " ")
		cases = append(cases,
			CatByteCase{Name: "all bytes across reads " + label, Input: strings.Repeat(string(all), 513), Args: flags},
			CatByteCase{Name: "CR across reads " + label, Parts: []string{strings.Repeat("\xff", 131071) + "\r\n\x80\r"}, Args: flags},
			CatByteCase{Name: "CR across files " + label, Parts: []string{"\xff\r", "", "\n\x80\r", "\xff"}, Args: flags},
			CatByteCase{Name: "blank across reads " + label, Parts: []string{strings.Repeat("\n", 131073) + "\xff\n\n\x80"}, Args: flags},
			CatByteCase{Name: "blank across files " + label, Parts: []string{"\xff\n\n", "", "\n\n\x80", "\xff\n"}, Args: flags},
		)
	}
	return cases
}

func RunCatByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "cat"
	if runtime.GOOS == "darwin" {
		name = "gcat"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "cat")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU cat")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU cat")
	}
	fields := strings.Fields(strings.SplitN(string(version), "\n", 2)[0])
	if len(fields) == 0 {
		t.Fatal("missing GNU cat version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU cat version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range CatByteCases() {
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
