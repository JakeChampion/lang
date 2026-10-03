package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func RunSplitByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "split"
	if runtime.GOOS == "darwin" {
		name = "gsplit"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "split")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU split")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU split")
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	type testCase struct {
		name string
		args []string
		data []byte
	}
	var cases []testCase
	modes := [][]string{
		{"-b", "32769"}, {"-l", "257"}, {"-C", "65537"},
		{"-n", "7"}, {"-n", "2/7"}, {"-n", "l/7"},
		{"-n", "l/2/7"}, {"-n", "r/7"}, {"-n", "r/3/7"},
		{"-l", "257", "-t", `\0`},
	}
	for _, args := range modes {
		for _, n := range []int{0, 1, 65535, 65536, 65537, 262143, 262144, 262145} {
			data := bytes.Repeat(all, (n+255)/256)[:n]
			cases = append(cases, testCase{fmt.Sprintf("%s/%d", strings.Join(args, "_"), n), args, data})
		}
	}
	for _, n := range []int{65535, 65536, 65537, 262145} {
		data := append(bytes.Repeat([]byte{0xff}, n), '\n', 0x80)
		for _, args := range [][]string{{"-C", "32769"}, {"-C", "524289"}, {"-n", "r/3"}, {"-n", "r/2/3"}} {
			cases = append(cases, testCase{fmt.Sprintf("long-record/%s/%d", strings.Join(args, "_"), n), args, data})
		}
	}
	for _, terminated := range []bool{false, true} {
		data := append([]byte{0x80, '\n'}, bytes.Repeat([]byte{0xff}, 524288)...)
		if terminated {
			data = append(data, '\n', 0xfe, '\n')
		}
		cases = append(cases, testCase{fmt.Sprintf("partial-file-long-record/terminated=%t", terminated), []string{"-C", "524289"}, data})
	}
	for _, tc := range cases {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.name, file), func(t *testing.T) {
				type result struct {
					code   int
					out    string
					err    string
					pieces map[string]string
				}
				var want result
				for _, impl := range []struct {
					name, bin string
					runner    []string
				}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
					dir := t.TempDir()
					if err := os.Mkdir(filepath.Join(dir, "scratch"), 0o700); err != nil {
						t.Fatal(err)
					}
					input := "-"
					if file {
						input = "input"
						if err := os.WriteFile(filepath.Join(dir, input), tc.data, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					argv := append(append(append([]string{}, impl.runner...), impl.bin), tc.args...)
					argv = append(argv, input, "piece")
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
					cmd.Dir = dir
					cmd.Env = append(os.Environ(), "LC_ALL=C", "TMPDIR=scratch")
					if !file {
						cmd.Stdin = bytes.NewReader(tc.data)
					}
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					err := cmd.Run()
					if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 {
						t.Fatalf("%s: %v\n%s", impl.name, err, diagnostic.Bytes())
					}
					if impl.name == "fern" && census != nil {
						census(t, diagnostic.String())
					}
					var clean strings.Builder
					for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
						if !strings.HasPrefix(line, "leakcheck:") && !strings.HasPrefix(line, "fern-sanitizer: leak ") {
							clean.WriteString(line)
						}
					}
					got := result{code: cmd.ProcessState.ExitCode(), out: out.String(), err: clean.String(), pieces: map[string]string{}}
					got.err = strings.ReplaceAll(got.err, impl.bin+": ", "split: ")
					got.err = strings.ReplaceAll(got.err, filepath.Base(impl.bin)+": ", "split: ")
					paths, err := filepath.Glob(filepath.Join(dir, "piece*"))
					if err != nil {
						t.Fatal(err)
					}
					for _, path := range paths {
						data, err := os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						got.pieces[filepath.Base(path)] = string(data)
					}
					if impl.name == "gnu" {
						if got.code != 0 {
							t.Fatalf("GNU fixture failed: %s", got.err)
						}
						want = got
					} else if !reflect.DeepEqual(got, want) {
						t.Fatalf("got status=%d stdout=%d stderr=%q files=%d; want status=%d stdout=%d stderr=%q files=%d (file names and bytes must match)", got.code, len(got.out), got.err, len(got.pieces), want.code, len(want.out), want.err, len(want.pieces))
					}
				}
			})
		}
	}
	t.Run("streaming-round-robin", func(t *testing.T) {
		for _, impl := range []struct {
			name, bin string
			runner    []string
		}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
			t.Run(impl.name, func(t *testing.T) {
				runSplitStreaming(t, impl.bin, impl.runner, census, impl.name == "fern")
			})
		}
	})
}

func runSplitStreaming(t *testing.T, bin string, runner []string, census func(*testing.T, string), checkCensus bool) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	argv := append(append([]string{}, runner...), bin, "-n", "r/2", "-", "piece")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		input.Close()
		cancel()
		if !waited {
			cmd.Wait()
		}
	}()
	// One whole round-robin read block, with stdin deliberately left open.
	data := bytes.Repeat([]byte{0xff, '\n'}, 131072)
	written := make(chan error, 1)
	go func() {
		_, err := input.Write(data)
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("split did not consume the input block")
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		ready := true
		for _, name := range []string{"pieceaa", "pieceab"} {
			info, err := os.Stat(filepath.Join(dir, name))
			ready = ready && err == nil && info.Size() > 0
		}
		if ready {
			break
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatal("round-robin output remained empty while stdin was open")
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || out.Len() != 0 {
		t.Fatalf("split: %v stdout=%q stderr=%q", err, out.String(), diagnostic.String())
	}
	if checkCensus && census != nil {
		census(t, diagnostic.String())
	}
	for _, name := range []string{"pieceaa", "pieceab"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, data[:len(data)/2]) {
			t.Fatalf("streamed %s: %v; bytes=%d, want=%d", name, err, len(got), len(data)/2)
		}
	}
}
