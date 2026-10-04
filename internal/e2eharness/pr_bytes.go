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

func RunPrByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "pr"
	if runtime.GOOS == "darwin" {
		name = "gpr"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "pr")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU pr")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU pr")
	}
	type testCase struct {
		name, data string
		args       []string
		rawArg     bool
		files      []string
	}
	var cases []testCase
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	modes := []struct {
		name string
		args []string
	}{
		{"page", nil}, {"no header", []string{"-t"}},
		{"no pagination", []string{"-T"}}, {"across", []string{"-3", "-a"}},
		{"down", []string{"-3"}}, {"numbered", []string{"-n", "-3", "-a"}},
		{"control", []string{"-c"}}, {"nonprinting", []string{"-v"}},
		{"tabs", []string{"-e4", "-i4", "-2"}},
		{"clipped", []string{"-W19", "-2", "-n"}},
		{"double spaced", []string{"-d", "-l17"}},
		{"last page", []string{"+1:1", "-l17"}},
	}
	inputs := []struct{ name, data string }{
		{"empty", ""}, {"all bytes", string(all)},
		{"form feeds", "\f\f\xff\n\x80\f\n\f\xc3x\f\f"},
		{"columns", strings.Repeat("a\t\xff\b\x80\x00tail\n", 67)},
		{"unterminated", "\xff\xc3x\x00\x80"},
	}
	for _, mode := range modes {
		for _, input := range inputs {
			cases = append(cases, testCase{name: mode.name + "/" + input.name, data: input.data, args: mode.args})
		}
	}
	for _, n := range []int{4095, 4096, 4097, 65535, 65536, 65537, 131073} {
		data := strings.Repeat("\xff", n) + "\nlast\x80"
		cases = append(cases,
			testCase{name: fmt.Sprintf("long %d", n), data: data, args: []string{"-t"}},
			testCase{name: fmt.Sprintf("long clipped %d", n), data: data, args: []string{"-t", "-W19"}},
			testCase{name: fmt.Sprintf("long form feed %d", n), data: strings.Repeat("x", n) + "\f\xff\n", args: []string{"-l17"}})
	}
	for i, data := range []string{"\b\bA\b\bB\n", "a\f\nb\n", "\f", "\f\f", "a\f\f", "\f\fa\nb\f\n\fX\f\f", "a\nb\n\f\nc\n", "a\f\nb\f\nc\f\n", "a\n\f\nb", "\b\t\bX\n", "a\b\b\tX\n"} {
		for j, args := range [][]string{{"-t"}, {"-T"}, {"-n", "-t"}, {"-3", "-t"}, {"-3", "-a", "-t"}, {"-l11"}, {"-d", "-l11"}, {"-l12", "+2:3"}, {"-m", "-t", "-", "-"}} {
			cases = append(cases, testCase{name: fmt.Sprintf("page event %d mode %d", i, j), data: data, args: args})
		}
	}
	for _, n := range []int{65535, 65536} {
		for _, args := range [][]string{{"-t"}, {"-l17"}} {
			cases = append(cases, testCase{name: fmt.Sprintf("form feed newline boundary %d %v", n, args), data: strings.Repeat("x", n) + "\f\n\xff\n", args: args})
		}
	}
	cases = append(cases,
		testCase{name: "recursive date format", args: []string{"-D", "%x"}, files: []string{"\xff\n"}},
		testCase{name: "merge empty form feed", args: []string{"-m", "-l17"}, files: []string{"\f", "a\nb\n"}},
		testCase{name: "directory read error", args: []string{"-t", "."}},
		testCase{name: "missing before raw file", args: []string{"-t", "missing"}, files: []string{"\xff\n\x80\n"}},
		testCase{name: "merge missing and raw file", args: []string{"-t", "-m", "missing"}, files: []string{"\xff\n\x80\n"}},
		testCase{name: "raw number separator", data: "\xff\n\x80\n", args: []string{"-t", "-n\xff3"}, rawArg: true},
		testCase{name: "raw output tab", data: "a\nb\n", args: []string{"-t", "-o16", "-i\xff4"}, rawArg: true},
		testCase{name: "merge bytes", args: []string{"-m", "-t"}, files: []string{string(all), "\xff\n\x00\n\x80"}},
		testCase{name: "merge pages", args: []string{"-m", "-l17", "-n"}, files: []string{strings.Repeat("\xff\n", 20), "\xc3x\f\n\x80\n"}},
		testCase{name: "serial files", args: []string{"-t"}, files: []string{"\xff\n\xc3", "\x80\x00\nlast"}})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rawArg && len(runner) > 0 && filepath.Base(runner[0]) == "wasmtime" {
				t.Skip("wasmtime requires UTF-8 argv before the guest starts; malformed bytes remain covered on stdin")
			}
			dir := t.TempDir()
			args := append([]string{"-D", "DATE", "-h", "HEADER"}, tc.args...)
			for i, data := range tc.files {
				name := fmt.Sprintf("input%d", i)
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, name)
			}
			var wantOut, wantDiagnostic string
			wantCode := 0
			for _, impl := range []struct {
				name, bin string
				runner    []string
			}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
				argv := append(append(append([]string{}, impl.runner...), impl.bin), args...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
				cmd.Stdin = strings.NewReader(tc.data)
				var out, diagnostic bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &diagnostic
				err := cmd.Run()
				if ctx.Err() != nil || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() < 0 {
					t.Fatalf("%s: %v\n%s", impl.name, err, diagnostic.Bytes())
				}
				code := cmd.ProcessState.ExitCode()
				if impl.name == "fern" && census != nil {
					census(t, diagnostic.String())
				}
				var clean strings.Builder
				for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
					if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
						continue
					}
					clean.WriteString(line)
				}
				diag := strings.ReplaceAll(clean.String(), impl.bin+": ", "pr: ")
				diag = strings.ReplaceAll(diag, filepath.Base(impl.bin)+": ", "pr: ")
				if impl.name == "gnu" {
					wantOut, wantDiagnostic, wantCode = out.String(), diag, code
				} else if code != wantCode || out.String() != wantOut || diag != wantDiagnostic {
					t.Fatalf("got status=%d output=%d prefix=%q stderr=%q; want status=%d output=%d prefix=%q stderr=%q", code, out.Len(), out.String()[:min(128, out.Len())], diag, wantCode, len(wantOut), wantOut[:min(128, len(wantOut))], wantDiagnostic)
				}
			}
		})
	}
}
