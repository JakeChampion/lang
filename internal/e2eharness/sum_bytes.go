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

type SumByteCase struct {
	Name, Input string
	Args        []string
	Parts       []string
}

func SumByteCases() []SumByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	var cases []SumByteCase
	for _, size := range []int{0, 1, 511, 512, 513, 1023, 1024, 1025, 65535, 65536, 65537, 131073} {
		input := string(bytes.Repeat(all, (size+255)/256)[:size])
		for _, mode := range []string{"-r", "-s"} {
			label := fmt.Sprintf("%s size %d", mode, size)
			cases = append(cases,
				SumByteCase{Name: label + " pipe", Input: input, Args: []string{mode}},
				SumByteCase{Name: label + " file", Parts: []string{input}, Args: []string{mode}},
			)
		}
	}
	for _, mode := range []string{"-r", "-s"} {
		cases = append(cases, SumByteCase{Name: mode + " separate files", Args: []string{mode},
			Parts: []string{string(all), "", string(all) + "\xff"}})
	}
	return cases
}

func RunSumByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "sum"
	if runtime.GOOS == "darwin" {
		name = "gsum"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "sum")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU sum")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU sum")
	}
	fields := strings.Fields(strings.SplitN(string(version), "\n", 2)[0])
	if len(fields) == 0 {
		t.Fatal("missing GNU sum version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU sum version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range SumByteCases() {
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
