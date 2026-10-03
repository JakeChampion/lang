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

type UniqByteCase struct {
	Name, Data string
	Args       []string
	Output     bool
}

func UniqByteCases() []UniqByteCase {
	var cases []UniqByteCase
	for _, term := range []byte{'\n', 0} {
		var all strings.Builder
		for b := 0; b < 256; b++ {
			for range 2 {
				all.WriteByte(byte(b))
				all.WriteByte('x')
				all.WriteByte(term)
			}
		}
		prefix := fmt.Sprintf("term %d", term)
		var mode []string
		if term == 0 {
			mode = []string{"-z"}
		}
		cases = append(cases, UniqByteCase{Name: prefix + " all bytes", Data: all.String(), Args: mode})
		rows := []string{"\xffA \xc3x", "\xffA \xc3x", "\xffa \xc3x", "\x80B \x00z", "\x80B \x00z", "\xfeC \xffv", ""}
		data := strings.Join(rows, string(term))
		for _, flags := range [][]string{
			nil, {"-c"}, {"-d"}, {"-u"}, {"-D"}, {"-D", "-i"},
			{"--all-repeated=prepend"}, {"--all-repeated=separate"},
			{"--group=prepend"}, {"--group=append"}, {"--group=separate"}, {"--group=both"},
			{"-f1"}, {"-s1"}, {"-w0"}, {"-w3"}, {"-f1", "-s1", "-w1", "-i"},
			{"-f18446744073709551615"}, {"-s18446744073709551615"}, {"-w18446744073709551615"},
		} {
			args := append(append([]string{}, mode...), flags...)
			cases = append(cases, UniqByteCase{Name: prefix + " " + strings.Join(flags, " "), Data: data, Args: args})
		}
		cases = append(cases, UniqByteCase{Name: prefix + " output file", Data: data, Args: append(append([]string{}, mode...), "-c"), Output: true})
		for _, n := range []int{65535, 65536, 65537} {
			line := strings.Repeat("\xff", n) + string(term)
			cases = append(cases, UniqByteCase{Name: fmt.Sprintf("%s boundary %d", prefix, n), Data: line + line + "\xfex", Args: mode})
		}
		cases = append(cases, UniqByteCase{Name: prefix + " held range across reads", Data: strings.Repeat("a"+string(term), 32767) + "\xffx" + string(term) + "\xffx", Args: append(append([]string{}, mode...), "-s1")})
	}
	cases = append(cases, UniqByteCase{Name: "empty"}, UniqByteCase{Name: "empty lines", Data: "\n\n\n"}, UniqByteCase{Name: "unterminated duplicate", Data: "\xffx\n\xffx"})
	return cases
}

func RunUniqByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "uniq"
	if runtime.GOOS == "darwin" {
		name = "guniq"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "uniq")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU uniq")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU uniq")
	}
	for _, tc := range UniqByteCases() {
		for _, file := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/file=%t", tc.Name, file), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "input"), []byte(tc.Data), 0o644); err != nil {
					t.Fatal(err)
				}
				var want []byte
				for _, impl := range []struct {
					name, bin string
					runner    []string
				}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
					args := append([]string{}, tc.Args...)
					if file {
						args = append(args, "input")
					} else if tc.Output {
						args = append(args, "-")
					}
					if tc.Output {
						args = append(args, impl.name+"-output")
					}
					command := append(append([]string{}, impl.runner...), impl.bin)
					command = append(command, args...)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, command[0], command[1:]...)
					cmd.Dir, cmd.Env = dir, append(os.Environ(), "LC_ALL=C")
					if !file {
						cmd.Stdin = strings.NewReader(tc.Data)
					}
					var stderr bytes.Buffer
					cmd.Stderr = &stderr
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("%s: %v\n%s", impl.name, err, stderr.Bytes())
					}
					if impl.name == "fern" && census != nil {
						census(t, stderr.String())
					} else if stderr.Len() != 0 {
						t.Fatalf("%s stderr: %s", impl.name, stderr.Bytes())
					}
					if tc.Output {
						if len(out) != 0 {
							t.Fatalf("%s unexpected stdout: %q", impl.name, out)
						}
						out, err = os.ReadFile(filepath.Join(dir, impl.name+"-output"))
						if err != nil {
							t.Fatal(err)
						}
					}
					if impl.name == "gnu" {
						want = out
					} else if !bytes.Equal(out, want) {
						t.Fatalf("output differs: got %d bytes, want %d", len(out), len(want))
					}
				}
			})
		}
	}
}
