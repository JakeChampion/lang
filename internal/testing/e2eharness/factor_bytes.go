package e2eharness

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func RunFactorByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "factor"
	if runtime.GOOS == "darwin" {
		name = "gfactor"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "factor")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU factor")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU factor")
	}
	type testCase struct {
		name, data string
		args       []string
		rawArg     bool
	}
	cases := []testCase{
		{name: "empty"},
		{name: "separators", data: " 12\t15\n21 42\t"},
		{name: "NUL token end", data: "12\x00\xff 15\x00x\n\x00\xff\n21"},
		{name: "invalid UTF-8", data: "\xff\n\xc3x\n12\x80\n21\n"},
		{name: "utf8 diagnostics", data: "é\nλ\n😀\n"},
		{name: "control diagnostics", data: "\r12\n\v12\n\f12\n\\'\n"},
		{name: "argument compatibility", args: []string{"--", "  +0012", "21", "0", "1", "-1", "é"}},
		{name: "raw argument", args: []string{"--", "\xff"}, rawArg: true},
	}
	var all strings.Builder
	for b := 0; b < 256; b++ {
		all.WriteByte(byte(b))
		all.WriteString("2\n")
	}
	cases = append(cases, testCase{name: "every byte", data: all.String()})
	for _, n := range []int{65535, 65536, 65537, 131073} {
		cases = append(cases,
			testCase{name: fmt.Sprintf("split digits %d", n), data: strings.Repeat("0", n) + "2\n42"},
			testCase{name: fmt.Sprintf("split invalid %d", n), data: strings.Repeat("0", n) + "\xff\n42"})
	}
	var wide strings.Builder
	for _, bits := range []uint{64, 96, 127} {
		wide.WriteString(new(big.Int).Lsh(big.NewInt(1), bits).String())
		wide.WriteByte('\n')
	}
	cases = append(cases, testCase{name: "wide powers", data: wide.String()}, testCase{name: "wide exponent powers", data: wide.String(), args: []string{"-h"}}, testCase{name: "many short", data: strings.Repeat("12 15 21 42\n", 8192)})
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
				diag := strings.ReplaceAll(clean.String(), impl.bin+": ", "factor: ")
				diag = strings.ReplaceAll(diag, filepath.Base(impl.bin)+": ", "factor: ")
				if impl.name == "gnu" {
					wantOut, wantDiagnostic, wantCode = out.String(), diag, code
				} else if code != wantCode || out.String() != wantOut || diag != wantDiagnostic {
					t.Fatalf("got status=%d output=%d stderr=%d; want status=%d output=%d stderr=%d", code, out.Len(), len(diag), wantCode, len(wantOut), len(wantDiagnostic))
				}
			}
		})
	}
}
