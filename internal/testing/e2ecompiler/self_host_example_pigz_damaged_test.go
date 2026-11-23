// CI-DARK: FERN_PIGZ_UPDATE — This flag regenerates the reference data.
package e2ecompiler

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const pigzDamagedGolden = "../../../examples/pigz/testdata/pigz-2.8-damaged.txt"

// pigzDamagedModes are the runs of each damaged stream.
var pigzDamagedModes = [][]string{{"-dc"}, {"-t"}, {"-l"}, {"-ltv"}}

// pigzDamage is one damaged copy of a stream.
type pigzDamage struct {
	name string
	ext  string
	data []byte
}

// pigzDamagedStreams are the streams that the corpus damages: one of each
// format and header that the port decodes. compress runs the port in dir.
func pigzDamagedStreams(t *testing.T, compress func(dir string, args ...string) []byte) map[string][]byte {
	t.Helper()
	gen := t.TempDir()
	corpus := pigzCorpus()[:60000]
	pigzWrite(t, filepath.Join(gen, "a"), corpus, 0o644)
	pigzWrite(t, filepath.Join(gen, "hi"), []byte("hello, hello, hello\n"), 0o644)
	streams := map[string][]byte{
		"g.gz":  compress(gen, "-c", "-p", "2", "-b", "32", "a"),
		"n.gz":  compress(gen, "-c", "-N", "-C", "a note", "hi"),
		"z.zz":  compress(gen, "-c", "-z", "-p", "1", "a"),
		"k.zip": compress(gen, "-c", "-K", "-p", "1", "hi"),
	}
	streams["m.gz"] = append(append([]byte{}, streams["n.gz"]...), streams["g.gz"]...)

	// A gzip header with each optional field: FEXTRA, FNAME, FCOMMENT and
	// FHCRC. The body and the trailer are those of a header with no fields.
	plain := compress(gen, "-c", "-n", "hi")
	h := []byte{0x1f, 0x8b, 8, 0x1e, 0, 0, 0, 0, 0, 3, 4, 0, 'a', 'b', 0, 0}
	h = append(h, "name\x00note\x00"...)
	h = binary.LittleEndian.AppendUint16(h, uint16(crc32.ChecksumIEEE(h)))
	streams["h.gz"] = append(h, plain[10:]...)

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "entry.txt", Method: zip.Store, Modified: pigzStamp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(corpus[:300]); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	streams["d.zip"] = buf.Bytes()

	lzw, err := os.ReadFile("../../../examples/pigz/testdata/lines.Z")
	if err != nil {
		t.Fatal(err)
	}
	streams["l.Z"] = lzw
	return streams
}

// pigzEdge says whether offset i of a stream of n bytes is damaged. Each
// offset in the first and the last 64 bytes is, and one offset in each
// stride between them.
func pigzEdge(i, n, stride int) bool {
	return i < 64 || i >= n-64 || i%stride == 0
}

// pigzDamaged is the corpus: each stream cut at many lengths, and with one
// byte changed at many offsets.
func pigzDamaged(streams map[string][]byte) []pigzDamage {
	names := make([]string, 0, len(streams))
	for name := range streams {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []pigzDamage
	for _, name := range names {
		data := streams[name]
		ext := name[strings.IndexByte(name, '.'):]
		for n := 0; n < len(data); n++ {
			if pigzEdge(n, len(data), 211) {
				out = append(out, pigzDamage{fmt.Sprintf("%s cut %d", name, n), ext, data[:n]})
			}
		}
		for i := range data {
			if !pigzEdge(i, len(data), 307) {
				continue
			}
			for _, x := range []byte{0x01, 0xff} {
				v := append([]byte{}, data...)
				v[i] ^= x
				out = append(out, pigzDamage{fmt.Sprintf("%s xor %d %02x", name, i, x), ext, v})
			}
		}
	}
	return out
}

// pigzDamagedRun runs a CLI on one damaged stream, named f with the
// suffix of its format, and gives the exit code, the standard error and a
// digest of the standard error and the standard output.
func pigzDamagedRun(t *testing.T, cli func(args ...string) *exec.Cmd, dir string, d pigzDamage, mode []string) (int, string, string) {
	t.Helper()
	file := "f" + d.ext
	if err := os.WriteFile(filepath.Join(dir, file), d.data, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := cli(append(append([]string{}, mode...), file)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TZ=UTC", "GZIP=", "PIGZ=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", d.name, err)
	}
	timer := time.AfterFunc(60*time.Second, func() { _ = cmd.Process.Kill() })
	_ = cmd.Wait()
	timer.Stop()
	h := sha256.New()
	h.Write(stderr.Bytes())
	h.Write([]byte{0})
	h.Write(stdout.Bytes())
	return cmd.ProcessState.ExitCode(), stderr.String(), hex.EncodeToString(h.Sum(nil)[:6])
}

// TestSelfHostExamplePigzDamaged runs the port on truncated and corrupt
// streams of each format it decodes, with -dc, -t, -l and -ltv, on x86-64
// and arm64.
// Each run must give the exit code, the messages and the output of pigz
// 2.8. testdata/pigz-2.8-damaged.txt holds what pigz 2.8 gave: a line for
// each damaged stream, with the exit code and a digest of the standard
// error and the standard output for each mode. When
// FERN_PIGZ_ORACLE names a pigz 2.8 binary, the test runs it as well, and
// with FERN_PIGZ_UPDATE=1 it then writes the file again.
func TestSelfHostExamplePigzDamaged(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/pigz/pigz.fern")
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string][]string{}
	if data, err := os.ReadFile(pigzDamagedGolden); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			fields := strings.Split(sc.Text(), "\t")
			if len(fields) == len(pigzDamagedModes)+1 {
				golden[fields[0]] = fields[1:]
			}
		}
	}
	oracle := os.Getenv("FERN_PIGZ_ORACLE")
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var port func(args ...string) *exec.Cmd
			if target == "x86-64-linux" {
				bin := pigzNamed(t, cli.x86Binary(t, src))
				port = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			} else {
				_, qemu := arm64Tooling(t)
				bin := pigzNamed(t, cli.arm64Binary(t, src))
				port = func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
			}
			streams := pigzDamagedStreams(t, func(dir string, args ...string) []byte {
				cmd := port(args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "TZ=UTC")
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("pigz %s: %v", strings.Join(args, " "), err)
				}
				return out
			})
			dir := t.TempDir()
			var lines []string
			failures := 0
			for _, d := range pigzDamaged(streams) {
				recs, ok := golden[d.name]
				if oracle != "" {
					recs, ok = nil, true
				}
				if !ok {
					t.Errorf("%s: no record of pigz 2.8 in %s", d.name, pigzDamagedGolden)
					continue
				}
				for i, mode := range pigzDamagedModes {
					exit, stderr, digest := pigzDamagedRun(t, port, dir, d, mode)
					got := fmt.Sprintf("%d %s", exit, digest)
					wantStderr := ""
					if oracle != "" {
						oexit, ostderr, odigest := pigzDamagedRun(t, func(args ...string) *exec.Cmd { return exec.Command(oracle, args...) }, dir, d, mode)
						recs = append(recs, fmt.Sprintf("%d %s", oexit, odigest))
						wantStderr = ostderr
					}
					if got != recs[i] {
						failures++
						if failures <= 20 {
							t.Errorf("pigz %s on %s:\n  got  %s, stderr %q\n  want %s, stderr %q", strings.Join(mode, " "), d.name, got, stderr, recs[i], wantStderr)
						}
					}
				}
				lines = append(lines, d.name+"\t"+strings.Join(recs, "\t"))
			}
			if failures > 20 {
				t.Errorf("%d more runs differ from pigz 2.8", failures-20)
			}
			if oracle != "" && os.Getenv("FERN_PIGZ_UPDATE") == "1" && target == "x86-64-linux" {
				if err := os.WriteFile(pigzDamagedGolden, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
