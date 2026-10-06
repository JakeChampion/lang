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

type FoldByteCase struct {
	Name, Input string
	Args        []string
	Parts       []string
}

func FoldByteCases() []FoldByteCase {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	input := "é\xff\t\x80\r\b\x00x\n" + string(all) + "\n\xff \x80\té"
	var cases []FoldByteCase
	for _, width := range []string{"1", "7", "8", "80"} {
		for _, mode := range [][]string{{}, {"-b"}, {"-s"}, {"-bs"}} {
			flags := append(append([]string{}, mode...), "-w", width)
			label := strings.Join(flags, " ")
			cases = append(cases,
				FoldByteCase{Name: label + " pipe", Input: input, Args: flags},
				FoldByteCase{Name: label + " file", Parts: []string{input}, Args: flags},
				FoldByteCase{Name: label + " empty", Args: flags},
				FoldByteCase{Name: label + " file boundaries", Parts: []string{"\xff\t\x80", "", "\b\r\x00é"}, Args: flags},
			)
		}
	}
	for _, flags := range [][]string{{"-w80"}, {"-bw80"}, {"-sw80"}, {"-bsw80"}} {
		label := strings.Join(flags, " ")
		long := strings.Repeat("\xff", 65535) + "\t\b\x00\r" + strings.Repeat("\x80 ", 32768) + "é"
		cases = append(cases,
			FoldByteCase{Name: "long terminated " + label, Parts: []string{long + "\n"}, Args: flags},
			FoldByteCase{Name: "long tail " + label, Parts: []string{long, "\xff\tabc\n"}, Args: flags},
		)
	}
	return cases
}

func RunFoldByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "fold"
	if runtime.GOOS == "darwin" {
		name = "gfold"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range append(filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")), "/opt/gnu-coreutils/bin") {
		candidate := filepath.Join(dir, "fold")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU fold")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU fold")
	}
	fields := strings.Fields(strings.SplitN(string(version), "\n", 2)[0])
	if len(fields) == 0 {
		t.Fatal("missing GNU fold version")
	}
	parts := strings.Split(fields[len(fields)-1], ".")
	if len(parts) < 2 {
		t.Fatalf("unrecognized GNU fold version: %s", fields[len(fields)-1])
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 9 || major == 9 && minor < 4 {
		t.Skip("requires GNU coreutils 9.4 or newer")
	}
	t.Logf("reference: %s (%s)", oracle, strings.SplitN(string(version), "\n", 2)[0])
	for _, tc := range FoldByteCases() {
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
