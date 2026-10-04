package e2eharness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

type TrByteCase struct {
	Name, Input string
	Args        []string
}

func TrByteCases() []TrByteCase {
	var all strings.Builder
	for i := range 256 {
		all.WriteByte(byte(i))
		all.WriteByte(byte(i))
	}
	sets := []struct {
		name string
		args []string
	}{
		{"identity", []string{`\000-\377`, `\000-\377`}},
		{"high mapping", []string{`\000-\177`, `\200-\377`}},
		{"low mapping", []string{`\200-\377`, `\000-\177`}},
		{"constant high", []string{`\000-\377`, `\377`}},
		{"class", []string{`[:lower:]`, `[:upper:]`}},
		{"repeat", []string{`\000-\377`, `[\377*256]`}},
		{"equivalence", []string{`[=\377=]`, `\200`}},
		{"truncate", []string{"-t", `\000-\377`, `\377\200`}},
		{"delete", []string{"-d", `\200-\377`}},
		{"delete complement", []string{"-cd", `\200-\377`}},
		{"delete all", []string{"-d", `\000-\377`}},
		{"squeeze", []string{"-s", `\000-\377`}},
		{"squeeze complement", []string{"-cs", `\200-\377`}},
		{"translate squeeze", []string{"-s", `\000-\377`, `\377`}},
		{"delete squeeze", []string{"-ds", `\000-\177`, `\200-\377`}},
	}
	var cases []TrByteCase
	for _, set := range sets {
		cases = append(cases, TrByteCase{Name: set.name, Input: all.String(), Args: set.args})
		cases = append(cases, TrByteCase{Name: set.name + " empty", Args: set.args})
	}
	for _, set := range sets {
		if !strings.Contains(set.name, "squeeze") {
			continue
		}
		cases = append(cases, TrByteCase{Name: set.name + " across reads", Input: strings.Repeat("\xff", 65535) + "\xff\x00\xff" + strings.Repeat(all.String(), 257), Args: set.args})
	}
	return cases
}

func RunTrByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "tr"
	if runtime.GOOS == "darwin" {
		name = "gtr"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "tr")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU tr")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU tr")
	}
	fields := strings.Fields(strings.SplitN(string(version), "\n", 2)[0])
	if len(fields) == 0 {
		t.Fatal("missing GNU tr version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU tr version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range TrByteCases() {
		t.Run(tc.Name, func(t *testing.T) {
			dir := t.TempDir()
			args := append([]string{}, tc.Args...)
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
