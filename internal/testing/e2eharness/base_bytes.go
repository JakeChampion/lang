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

// RunBaseByteCases checks the shared codec through each public utility.
// Expected bytes and error prefixes come from GNU, including malformed input.
func RunBaseByteCases(t *testing.T, utility, bin string, runner []string, census func(*testing.T, string)) {
	t.Helper()
	name := utility
	if runtime.GOOS == "darwin" {
		name = "g" + name
	}
	oracle, err := exec.LookPath(name)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		path := filepath.Join(dir, utility)
		if info, e := os.Stat(path); e == nil && !info.IsDir() {
			oracle, err = path, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU " + utility)
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU " + utility)
	}
	type testCase struct {
		name       string
		args       []string
		data       []byte
		fragmented bool
	}
	codecs := []string{""}
	if utility == "basenc" {
		codecs = []string{"--base64", "--base64url", "--base32", "--base32hex", "--base16", "--base2msbf", "--base2lsbf", "--z85", "--base58"}
	}
	var cases []testCase
	for _, codec := range codecs {
		var flags []string
		if codec != "" {
			flags = []string{codec}
		}
		sizes := []int{0, 256, 30724}
		if codec == "--base58" {
			sizes = []int{0, 256}
		}
		for _, n := range sizes {
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(i)
			}
			args := append(append([]string{}, flags...), "-w0")
			cmd := exec.Command(oracle, args...)
			cmd.Stdin, cmd.Env = bytes.NewReader(data), append(os.Environ(), "LC_ALL=C")
			encoded, err := cmd.Output()
			if err != nil {
				t.Fatalf("GNU %s encode %d: %v", codec, n, err)
			}
			prefix := fmt.Sprintf("%s/%d", codec, n)
			cases = append(cases,
				testCase{prefix + " encode", args, data, false},
				testCase{prefix + " wrap", append(append([]string{}, flags...), "-w7"), data, false},
				testCase{prefix + " decode", append(append([]string{}, flags...), "-d"), encoded, false})
			if n > 0 {
				garbage := append(append([]byte{}, encoded...), 255, 0, 128)
				cases = append(cases,
					testCase{prefix + " short final group", append(append([]string{}, flags...), "-d"), encoded[:len(encoded)-1], false},
					testCase{prefix + " garbage", append(append([]string{}, flags...), "-d"), garbage, false},
					testCase{prefix + " ignored garbage", append(append([]string{}, flags...), "-di"), garbage, false})
			}
			if n == 30724 {
				cases = append(cases,
					testCase{prefix + " fragmented encode", args, data, true},
					testCase{prefix + " fragmented decode", append(append([]string{}, flags...), "-d"), encoded, true})
			}
		}
		if codec == "--base58" {
			cases = append(cases, testCase{"base58 zero blocks", flags, make([]byte, 30724), true})
		}
	}
	for _, tc := range cases {
		for _, file := range []bool{false, true} {
			if file && tc.fragmented {
				continue
			}
			t.Run(fmt.Sprintf("%s/file=%t", tc.name, file), func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "input"), tc.data, 0o644); err != nil {
					t.Fatal(err)
				}
				var wantOut, wantDiagnostic []byte
				wantCode := 0
				for _, impl := range []struct {
					name, bin string
					runner    []string
				}{{"gnu", oracle, nil}, {"fern", bin, runner}} {
					args := append([]string{}, tc.args...)
					if file {
						args = append(args, "input")
					}
					argv := append(append(append([]string{}, impl.runner...), impl.bin), args...)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
					cmd.Dir, cmd.Env = dir, append(os.Environ(), "LC_ALL=C")
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					if tc.fragmented {
						pipe, err := cmd.StdinPipe()
						if err != nil {
							t.Fatal(err)
						}
						if err := cmd.Start(); err != nil {
							t.Fatal(err)
						}
						for start := 0; start < len(tc.data); {
							end := min(start+4093, len(tc.data))
							if _, err := pipe.Write(tc.data[start:end]); err != nil {
								break
							}
							start = end
							time.Sleep(time.Millisecond)
						}
						_ = pipe.Close()
						err = cmd.Wait()
					} else {
						if !file {
							cmd.Stdin = bytes.NewReader(tc.data)
						}
						err = cmd.Run()
					}
					if ctx.Err() != nil || cmd.ProcessState == nil {
						t.Fatalf("%s: %v\n%s", impl.name, err, diagnostic.Bytes())
					}
					code := cmd.ProcessState.ExitCode()
					if code < 0 {
						t.Fatalf("%s terminated abnormally: %v\n%s", impl.name, err, diagnostic.Bytes())
					}
					if impl.name == "fern" && census != nil && code == 0 {
						census(t, diagnostic.String())
					}
					var clean strings.Builder
					for _, line := range strings.SplitAfter(diagnostic.String(), "\n") {
						if strings.HasPrefix(line, "leakcheck:") || strings.HasPrefix(line, "fern-sanitizer: leak ") {
							if code != 0 && census != nil {
								t.Log(strings.TrimSpace(line))
							}
							continue
						}
						clean.WriteString(line)
					}
					diag := strings.ReplaceAll(clean.String(), impl.bin+": ", utility+": ")
					diag = strings.ReplaceAll(diag, filepath.Base(impl.bin)+": ", utility+": ")
					if impl.name == "gnu" {
						wantOut, wantDiagnostic, wantCode = append([]byte{}, out.Bytes()...), []byte(diag), code
					} else if code != wantCode || !bytes.Equal(out.Bytes(), wantOut) || !bytes.Equal([]byte(diag), wantDiagnostic) {
						t.Fatalf("got status=%d output=%d stderr=%q; want status=%d output=%d stderr=%q", code, out.Len(), diag, wantCode, len(wantOut), wantDiagnostic)
					}
				}
			})
		}
	}
}
