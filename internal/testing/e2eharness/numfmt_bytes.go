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

func RunNumfmtByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "numfmt"
	if runtime.GOOS == "darwin" {
		name = "gnumfmt"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "numfmt")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU numfmt")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU numfmt")
	}
	type testCase struct {
		name, data string
		args       []string
		rawArg     bool
	}
	cases := []testCase{
		{name: "empty"},
		{name: "abort after output", data: "1000 2000\n3000 x 4000\n5000\n", args: []string{"--field=-", "--to=si", "--debug"}},
		{name: "abort operands", args: []string{"--to=si", "--debug", "1000", "x", "2000"}},
		// Exact scaled integers have the same digits on the host GNU oracle
		// and guest targets with different long-double formats.
		{name: "long format stdin", data: "1000\n999000\n1000\n", args: []string{"--to=si", "--format=%.123f"}},
		{name: "long format fields", args: []string{"--field=-", "--to=si", "--format=%.123f", "1000 999000 1000"}},
		{name: "precision refusal stdin", data: "1000\n2000\n", args: []string{"--format=%.100f"}},
		{name: "precision refusal operands", args: []string{"--format=%.100f", "1000", "2000"}},
		{name: "raw unselected", data: "1000 \xff\xc3x\n2000 \x80\n", args: []string{"--to=si"}},
		{name: "raw first field", data: "\xff,1000\n\xc3x,2000", args: []string{"-d,", "--field=2", "--to=si"}},
		{name: "raw header", data: "\xff\xc3x\n1000\n", args: []string{"--header", "--to=si"}},
		{name: "unterminated header", data: "\xff\xc3x", args: []string{"--header"}},
		{name: "zero header", data: "\xff\xc3x\x001000\x00", args: []string{"-z", "--header", "--to=si"}},
		{name: "NUL truncation", data: "1000\x00\xff\n2000\x00\xc3x", args: []string{"--to=si"}},
		{name: "header NUL truncation", data: "\xff\x00hidden\n1000\n", args: []string{"--header", "--to=si"}},
		{name: "NUL fields", data: "1000\x00\xff\x00", args: []string{"-z", "-d", "", "--invalid=ignore", "--to=si"}},
		{name: "raw delimiter", data: "1000\xffraw\n", args: []string{"-d", "\xff", "--to=si"}, rawArg: true},
	}
	for _, mode := range []string{"ignore", "warn", "fail", "abort"} {
		for _, scale := range []string{"si", "iec", "iec-i"} {
			cases = append(cases, testCase{name: "scaled overflow " + mode + " " + scale, data: "1Q\n2000Q\n-2000Q\n1Q\n", args: []string{"--from=si", "--to=" + scale, "--invalid=" + mode}})
		}
		for _, input := range []string{"\xff\n1000\n", "1K\xff\n1000\n", "é\nλ\n😀\n", "'\\\x01\x80\n1000\n"} {
			cases = append(cases, testCase{name: fmt.Sprintf("invalid %s %x", mode, input), data: input, args: []string{"--from=auto", "--invalid=" + mode}})
		}
	}
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("\n")
	}
	cases = append(cases, testCase{name: "every byte", data: all.String(), args: []string{"--invalid=warn"}})
	for _, n := range []int{65535, 65536, 65537, 131073, 262145} {
		cases = append(cases,
			testCase{name: fmt.Sprintf("long header %d", n), data: strings.Repeat("\xff", n) + "\n1000\n", args: []string{"--header", "--to=si"}},
			testCase{name: fmt.Sprintf("long field %d", n), data: strings.Repeat("\xff", n) + ",1000\n", args: []string{"-d,", "--field=2", "--to=si"}},
			testCase{name: fmt.Sprintf("long unterminated %d", n), data: "1000," + strings.Repeat("\xff", n), args: []string{"-d,", "--to=si"}},
			testCase{name: fmt.Sprintf("split digits %d", n), data: strings.Repeat("0", n) + "2\n1000", args: []string{"--to=si"}},
			testCase{name: fmt.Sprintf("zero long header %d", n), data: strings.Repeat("\xff", n) + "\x001000\x00", args: []string{"-z", "--header", "--to=si"}})
	}
	cases = append(cases, testCase{name: "many short", data: strings.Repeat("1000 \xff\n", 8192), args: []string{"--to=si"}})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.rawArg && len(runner) > 0 && filepath.Base(runner[0]) == "wasmtime" {
				t.Skip("wasmtime requires UTF-8 argv before the guest starts; malformed bytes remain covered on stdin")
			}
			var wantOut, wantDiagnostic string
			wantCode := 0
			for _, impl := range []struct {
				name, bin string
				runner    []string
			}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
				argv := append(append(append([]string{}, impl.runner...), impl.bin), tc.args...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
				cmd.Env = append(os.Environ(), "LC_ALL=C")
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
				diag := strings.ReplaceAll(clean.String(), impl.bin+": ", "numfmt: ")
				diag = strings.ReplaceAll(diag, filepath.Base(impl.bin)+": ", "numfmt: ")
				if impl.name == "gnu" {
					wantOut, wantDiagnostic, wantCode = out.String(), diag, code
				} else if code != wantCode || out.String() != wantOut || diag != wantDiagnostic {
					t.Fatalf("got status=%d output=%d stderr=%d; want status=%d output=%d stderr=%d", code, out.Len(), len(diag), wantCode, len(wantOut), len(wantDiagnostic))
				}
			}
		})
	}
}
