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

func RunCksumByteCases(t *testing.T, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := "cksum"
	if runtime.GOOS == "darwin" {
		name = "gcksum"
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, "cksum")
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU cksum")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU cksum")
	}
	type testCase struct {
		name, data string
		args       []string
	}
	var cases []testCase
	all := make([]byte, 256)
	for at := range all {
		all[at] = byte(at)
	}
	for _, algorithm := range []string{"crc", "crc32b", "bsd", "sysv"} {
		for _, n := range []int{0, 1, 15, 16, 17, 127, 128, 129, 65535, 65536, 65537, 131073} {
			data := string(bytes.Repeat(all, (n+255)/256)[:n])
			for _, raw := range []bool{false, true} {
				args := []string{"-a", algorithm}
				if raw {
					args = append(args, "--raw")
				}
				cases = append(cases, testCase{name: fmt.Sprintf("%s/%d/raw=%t", algorithm, n, raw), data: data, args: args})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
				diag := strings.ReplaceAll(clean.String(), impl.bin+": ", "cksum: ")
				diag = strings.ReplaceAll(diag, filepath.Base(impl.bin)+": ", "cksum: ")
				if impl.name == "gnu" {
					wantOut, wantDiagnostic, wantCode = out.String(), diag, code
				} else if code != wantCode || out.String() != wantOut || diag != wantDiagnostic {
					t.Fatalf("got status=%d output=%d stderr=%d; want status=%d output=%d stderr=%d", code, out.Len(), len(diag), wantCode, len(wantOut), len(wantDiagnostic))
				}
			}
		})
	}
}
