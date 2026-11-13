package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostExamplePigzUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/pigz/gz_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// pigzRun runs the CLI in dir with stdin and gives its stdout, stderr and
// exit code.
func pigzRun(t *testing.T, pigz func(args ...string) *exec.Cmd, dir string, stdin []byte, args ...string) ([]byte, string, int) {
	t.Helper()
	cmd := pigz(args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("pigz did not start: %s", &stderr)
	}
	return stdout.Bytes(), stderr.String(), cmd.ProcessState.ExitCode()
}

func gunzipWithGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	cmd := exec.Command("gzip", "-dc")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gzip -dc: %v", err)
	}
	return out
}

func pigzCorpus() []byte {
	var b bytes.Buffer
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "line %d of the corpus, %d\n", i*7%3001, i*i%977)
	}
	return b.Bytes()
}

func pigzWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func pigzExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestSelfHostExamplePigzCLI checks the port against gzip, which reads what
// it writes and writes what it reads. The messages and the exit codes are
// those of pigz 2.8.
func TestSelfHostExamplePigzCLI(t *testing.T) {
	if _, err := exec.LookPath("gzip"); err != nil {
		t.Fatal("gzip is required: it is the oracle of the pigz example")
	}
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/pigz/pigz.fern")
	if err != nil {
		t.Fatal(err)
	}
	corpus := pigzCorpus()
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var pigz func(args ...string) *exec.Cmd
			if target == "x86-64-linux" {
				bin := cli.x86Binary(t, src)
				pigz = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			} else {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, src)
				pigz = func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
			}

			t.Run("levels", func(t *testing.T) {
				dir := t.TempDir()
				var sizes []int
				for _, level := range []string{"-1", "-6", "-9"} {
					out, stderr, code := pigzRun(t, pigz, dir, corpus, level)
					if code != 0 {
						t.Fatalf("pigz %s: exit %d: %s", level, code, stderr)
					}
					if !bytes.Equal(gunzipWithGzip(t, out), corpus) {
						t.Errorf("gzip -dc of pigz %s differs from the input", level)
					}
					sizes = append(sizes, len(out))
					gz := exec.Command("gzip", "-c", level)
					gz.Stdin = bytes.NewReader(corpus)
					theirs, err := gz.Output()
					if err != nil {
						t.Fatal(err)
					}
					back, stderr, code := pigzRun(t, pigz, dir, theirs, "-d")
					if code != 0 || !bytes.Equal(back, corpus) {
						t.Errorf("pigz -d of gzip %s: exit %d: %s", level, code, stderr)
					}
				}
				if sizes[2] > sizes[1] || sizes[1] > sizes[0] {
					t.Errorf("sizes at -1, -6 and -9: %v; want each no larger than the one before", sizes)
				}
			})

			t.Run("processes", func(t *testing.T) {
				dir := t.TempDir()
				if len(corpus) <= 3*131072 {
					t.Fatalf("the corpus has %d bytes, too few for three blocks", len(corpus))
				}
				var outs [][]byte
				for _, p := range []string{"1", "2", "4"} {
					for _, level := range []string{"-1", "-9"} {
						out, stderr, code := pigzRun(t, pigz, dir, corpus, level, "-p", p)
						if code != 0 {
							t.Fatalf("pigz %s -p %s: exit %d: %s", level, p, code, stderr)
						}
						if !bytes.Equal(gunzipWithGzip(t, out), corpus) {
							t.Errorf("gzip -dc of pigz %s -p %s differs from the input", level, p)
						}
						outs = append(outs, out)
					}
				}
				// The blocks do not depend on the count of workers.
				if !bytes.Equal(outs[2], outs[4]) || !bytes.Equal(outs[3], outs[5]) {
					t.Error("pigz -p 2 and pigz -p 4 must write the same bytes")
				}
			})

			t.Run("files", func(t *testing.T) {
				dir := t.TempDir()
				f := filepath.Join(dir, "f")
				pigzWrite(t, f, corpus, 0o640)
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "f"); code != 0 {
					t.Fatalf("pigz f: exit %d: %s", code, stderr)
				}
				if pigzExists(f) || !pigzExists(f+".gz") {
					t.Fatal("pigz f must write f.gz and remove f")
				}
				packed, err := os.ReadFile(f + ".gz")
				if err != nil {
					t.Fatal(err)
				}
				if packed[3] != 8 || !bytes.HasPrefix(packed[10:], []byte("f\x00")) {
					t.Errorf("the header does not keep the name: % x", packed[:12])
				}
				if st, err := os.Stat(f + ".gz"); err != nil || st.Mode().Perm() != 0o640 {
					t.Errorf("f.gz has mode %v, want 0640", st.Mode().Perm())
				}
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "-t", "f.gz"); code != 0 || stderr != "" {
					t.Errorf("pigz -t f.gz: exit %d: %s", code, stderr)
				}
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "-dk", "f.gz"); code != 0 {
					t.Fatalf("pigz -dk f.gz: exit %d: %s", code, stderr)
				}
				got, err := os.ReadFile(f)
				if err != nil || !bytes.Equal(got, corpus) || !pigzExists(f+".gz") {
					t.Fatal("pigz -dk f.gz must restore f and keep f.gz")
				}
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "-d", "f.gz"); code != 1 || stderr != "pigz: skipping: f exists\n" {
					t.Errorf("pigz -d f.gz over f: exit %d, stderr %q", code, stderr)
				}
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "-df", "f.gz"); code != 0 || pigzExists(f+".gz") {
					t.Errorf("pigz -df f.gz: exit %d: %s", code, stderr)
				}
				out, _, code := pigzRun(t, pigz, dir, nil, "-c", "f", "f")
				if code != 0 || !bytes.Equal(gunzipWithGzip(t, out), append(append([]byte{}, corpus...), corpus...)) {
					t.Error("pigz -c f f must write two members")
				}
				if !pigzExists(f) {
					t.Error("pigz -c must keep its input")
				}
				pigzWrite(t, filepath.Join(dir, "x.tgz"), packed, 0o644)
				if _, stderr, code := pigzRun(t, pigz, dir, nil, "-d", "x.tgz"); code != 0 || !pigzExists(filepath.Join(dir, "x.tar")) {
					t.Errorf("pigz -d x.tgz: exit %d: %s", code, stderr)
				}
			})

			t.Run("refusals", func(t *testing.T) {
				dir := t.TempDir()
				pigzWrite(t, filepath.Join(dir, "a"), []byte("hi\n"), 0o644)
				packed, _, _ := pigzRun(t, pigz, dir, corpus)
				n := len(packed)
				crc := append([]byte{}, packed...)
				crc[n-8] ^= 1
				length := append([]byte{}, packed...)
				length[n-4] ^= 1
				pigzWrite(t, filepath.Join(dir, "crc.gz"), crc, 0o644)
				pigzWrite(t, filepath.Join(dir, "len.gz"), length, 0o644)
				pigzWrite(t, filepath.Join(dir, "trunc.gz"), packed[:n-20], 0o644)
				pigzWrite(t, filepath.Join(dir, "tail.gz"), append(append([]byte{}, packed...), "garbage\n"...), 0o644)
				if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
					t.Fatal(err)
				}
				pigzWrite(t, filepath.Join(dir, "a.gz.gz"), packed, 0o644)
				for _, tc := range []struct {
					args   []string
					code   int
					stderr string
				}{
					{[]string{"-x"}, 22, "pigz: abort: invalid option: -x\n"},
					{[]string{"--bogus"}, 22, "pigz: abort: invalid option: --bogus\n"},
					{[]string{"-0", "a"}, 22, "pigz: abort: only levels 1..9 are allowed\n"},
					{[]string{"-p", "0", "a"}, 22, "pigz: abort: invalid number of processes: 0\n"},
					{[]string{"-p", "x", "a"}, 22, "pigz: abort: invalid numeric parameter: x\n"},
					{[]string{"a", "-p"}, 22, "pigz: abort: missing parameter after -p\n"},
					{[]string{"nosuch"}, 1, "pigz: skipping: nosuch does not exist\n"},
					{[]string{"d"}, 1, "pigz: skipping: d is a directory\n"},
					{[]string{"-d", "a"}, 1, "pigz: skipping: a does not have compressed suffix\n"},
					{[]string{"a.gz.gz"}, 0, "pigz: skipping: a.gz.gz ends with .gz\n"},
					{[]string{"-t", "crc.gz"}, 1, "pigz: skipping: crc.gz: corrupted -- crc32 mismatch\n"},
					{[]string{"-t", "len.gz"}, 1, "pigz: skipping: len.gz: corrupted -- length mismatch\n"},
					{[]string{"-t", "trunc.gz"}, 1, "pigz: skipping: trunc.gz: corrupted -- incomplete deflate data\n"},
					{[]string{"-t", "a"}, 1, "pigz: skipping: a unrecognized format\n"},
					{[]string{"-t", "tail.gz"}, 0, ""},
				} {
					_, stderr, code := pigzRun(t, pigz, dir, nil, tc.args...)
					if code != tc.code || stderr != tc.stderr {
						t.Errorf("pigz %s: exit %d, stderr %q; want exit %d, stderr %q", strings.Join(tc.args, " "), code, stderr, tc.code, tc.stderr)
					}
				}
				if !pigzExists(filepath.Join(dir, "a")) {
					t.Error("a refused run must not remove its input")
				}
			})
		})
	}
}
