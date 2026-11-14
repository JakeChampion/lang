// CI-DARK: FERN_PIGZ_ORACLE — This flag compares against a pigz 2.8 binary. CI compares against testdata/pigz-2.8.json.
// CI-DARK: FERN_PIGZ_UPDATE — This flag regenerates the reference data.
package e2ecompiler

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSelfHostExamplePigzUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/pigz/gz_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// pigzRecord is what one run of the CLI leaves: the exit code, the standard
// error, and a digest. The digest covers the standard output and each file
// in the directory, with its mode, size, time and link count.
type pigzRecord struct {
	Exit   int    `json:"exit"`
	Stderr string `json:"stderr"`
	Digest string `json:"digest"`
}

// pigzCase is one run of the CLI in a fresh copy of the test directory.
// stdin names a file of that directory, or is empty for /dev/null.
type pigzCase struct {
	name  string
	args  []string
	stdin string
	env   []string
}

func pc(name string, args ...string) pigzCase { return pigzCase{name: name, args: args} }

func pcIn(name, stdin string, args ...string) pigzCase {
	return pigzCase{name: name, args: args, stdin: stdin}
}

func pcEnv(name, env string, args ...string) pigzCase {
	return pigzCase{name: name, args: args, env: []string{env}}
}

// pigzCases are the runs that the port must give as pigz 2.8 gives them.
// Each run on an input of more than one block names -p, because one
// process and more processes write different bytes.
var pigzCases = []pigzCase{
	pc("keep", "-k", "-p", "4", "a"),
	pc("one process", "-k", "-p", "1", "a"),
	pc("level 1", "-1", "-k", "-p", "4", "a"),
	pc("level 9 one process", "-9", "-k", "-p", "1", "a"),
	pc("level 0", "-0", "-k", "-p", "4", "b"),
	pc("level 0 one process", "-0", "-k", "-p", "1", "b"),
	pc("huffman", "-H", "-k", "-p", "4", "a"),
	pc("rle", "-U", "-k", "-p", "4", "b"),
	pc("independent", "-i", "-k", "-p", "4", "a"),
	pc("independent one process", "-i", "-k", "-p", "1", "a"),
	pc("blocksize 32", "-b", "32", "-k", "-p", "4", "a"),
	pc("blocksize 1000", "-b", "1000", "-k", "-p", "1", "a"),
	pc("rsyncable", "-R", "-k", "-p", "4", "a"),
	pc("rsyncable one process", "-R", "-k", "-p", "1", "a"),
	pc("zlib", "-z", "-k", "-p", "4", "a"),
	pc("zip", "-K", "-k", "-p", "4", "a"),
	pc("default", "-k", "mid"),
	pc("replace", "mid"),
	pc("no name", "-n", "-k", "mid"),
	pc("no time", "-m", "-k", "mid"),
	pc("no time T", "-T", "-k", "mid"),
	pc("name", "-N", "-k", "mid"),
	pc("time", "-M", "-k", "mid"),
	pc("comment", "-C", "a note", "-k", "mid"),
	pc("zlib comment", "-z", "-C", "a note", "-k", "mid"),
	pc("zip comment", "-K", "-C", "a note", "-k", "mid"),
	pc("suffix", "-S", ".xx", "-k", "mid"),
	pc("stdout", "-c", "mid"),
	pc("two members", "-c", "-p", "2", "a", "a"),
	pc("two zip entries", "-K", "-c", "hi", "mid"),
	pc("verbose", "-v", "-k", "mid"),
	pc("synchronous", "-Y", "-k", "mid"),
	pc("recursive", "-r", "-k", "sub"),
	pc("symbolic link", "link"),
	// The time of a symbolic link is the time the test made it, so these
	// runs do not keep it.
	pc("force symbolic link", "-f", "-m", "-p", "4", "link"),
	pc("stdout symbolic link", "-c", "-m", "-p", "4", "link"),
	pc("hard link", "hard"),
	pc("force hard link", "-f", "hard"),
	pc("empty", "empty"),
	pc("missing", "nosuch"),
	pc("quiet missing", "-q", "nosuch"),
	pc("directory", "sub"),
	pc("ends with suffix", "pre.gz"),
	pc("force suffix", "-f", "-k", "pre.gz"),
	pcIn("stdin", "a", "-p", "4"),
	pcIn("stdin zip alias", "mid", "-K", "-A", "name.txt"),
	pcIn("stdin zlib", "mid", "-z"),
	pcIn("stdin no name", "mid", "-n"),
	pcIn("stdin one process", "mid", "-p", "1", "-9"),
	pcEnv("GZIP", "GZIP=-9 -n", "-k", "mid"),
	pcEnv("GZIP blank", "GZIP=-k  ", "mid"),
	pcEnv("GZIP file", "GZIP=mid", "-k", "mid"),
	pcEnv("PIGZ parameter", "PIGZ=-p", "mid"),
	pcEnv("PIGZ invalid", "PIGZ=-x", "-k", "mid"),
	pcEnv("GZIP and PIGZ", "PIGZ=-c -1", "-n", "mid"),

	pc("decompress", "-dk", "g.gz"),
	pc("decompress name", "-dkN", "n.gz"),
	pc("decompress name time", "-dkNT", "n.gz"),
	pc("decompress time", "-dkM", "n.gz"),
	pc("decompress zlib", "-dk", "z.zz"),
	pc("decompress zip", "-dk", "k.zip"),
	pc("decompress zip descriptor", "-dk", "zd.zip"),
	pc("decompress zip stored", "-dk", "zs.zip"),
	pc("decompress members", "-dk", "multi.gz"),
	pc("decompress tgz", "-dk", "x.tgz"),
	pc("decompress lzw", "-dk", "lines.Z"),
	pc("decompress lzw 9 bits", "-dk", "lines9.Z"),
	pc("decompress junk", "-dk", "junk.gz"),
	pc("decompress junk verbose", "-dkv", "junk.gz"),
	pc("decompress zlib junk", "-dk", "zjunk.zz"),
	pc("decompress exists", "-dk", "x.gz"),
	pc("decompress without suffix", "-dk", "mid"),
	pc("decompress finds suffix", "-d", "g"),
	pc("unpigz stdout", "-dc", "plain.gz"),
	pc("cat plain", "-dcf", "plain.gz"),
	pc("cat junk", "-dcf", "junk.gz"),
	pc("cat empty", "-dcf", "empty.gz"),
	pc("test truncated", "-t", "trunc.gz"),
	pc("test crc", "-t", "crc.gz"),
	pc("test length", "-t", "len.gz"),
	pc("test adler", "-t", "ad.zz"),
	pc("test bad data", "-t", "bad.gz"),
	pc("test lzw flags", "-t", "flags.Z"),
	pc("test lzw short", "-t", "half.Z"),
	pc("test plain", "-t", "plain.gz"),
	pc("test empty", "-t", "empty.gz"),
	pc("test each", "-t", "g.gz", "z.zz", "k.zip", "lines.Z", "multi.gz"),
	pcIn("stdin decompress", "g.gz", "-d"),
	pcIn("stdin decompress lzw", "lines.Z", "-d"),
	pcIn("stdin decompress zip", "k.zip", "-d"),

	pc("list", "-l", "g.gz"),
	pc("list each", "-l", "n.gz", "z.zz", "k.zip", "lines.Z", "multi.gz", "x.tgz", "cm.gz"),
	pc("list verbose", "-lv", "n.gz", "z.zz", "k.zip", "lines.Z", "multi.gz", "x.tgz", "cm.gz"),
	pc("list test", "-lt", "n.gz", "z.zz", "k.zip", "lines.Z", "multi.gz"),
	pc("list test verbose", "-ltv", "n.gz", "z.zz", "k.zip", "lines.Z", "multi.gz", "cm.gz"),
	pc("list refusals", "-l", "empty.gz", "plain.gz", "trunc.gz"),
	pc("list quiet", "-l", "-q", "g.gz"),
	pcIn("stdin list", "g.gz", "-l"),
	pcIn("stdin list zlib", "z.zz", "-lv"),
	pcIn("stdin list test", "multi.gz", "-ltv"),

	pc("invalid option", "-x"),
	pc("invalid long option", "--bogus"),
	pc("level 10", "-10"),
	pc("no processes", "-p", "0", "mid"),
	pc("bad number", "-p", "x", "mid"),
	pc("missing parameter", "mid", "-p"),
	pc("small block", "-b", "31", "mid"),
	pc("suffix space", "-Sx", "mid"),
	pc("comment space", "-Cx", "mid"),
	pc("LZW output", "-Z"),
	pc("ascii", "-a"),
	pc("long parameter", "--processes=2"),
	pc("version", "-V"),
	pc("verbose version", "-vV"),
	pc("license", "-L"),
	pc("help", "-h"),
	pc("quiet help", "-qh", "-k", "mid"),
	pc("quiet invalid", "-qx"),
	pc("best", "--best", "-k", "mid"),
	pc("fast", "--fast", "-k", "mid"),
	pc("dash", "-k", "-", "mid"),
	pc("zopfli settings", "-I", "3", "-J", "3", "-F", "-O", "-k", "mid"),
	pc("recursive decompress", "-dr", "subgz"),
	pc("recursive list", "-lr", "subgz"),
	pc("recursive test", "-tr", "subgz"),
}

// pigzStamp is the time of each file of the test directory.
var pigzStamp = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)

func pigzCorpus() []byte {
	var b bytes.Buffer
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "line %d of the corpus, %d\n", i*7%3001, i*i%977)
	}
	return b.Bytes()
}

// pigzBinary is a corpus of bytes that do not repeat, a run of zeros, and
// runs of each byte.
func pigzBinary() []byte {
	out := make([]byte, 0, 300000)
	x := uint32(2463534242)
	for len(out) < 150000 {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		out = append(out, byte(x))
	}
	out = append(out, make([]byte, 50000)...)
	for len(out) < 300000 {
		out = append(out, bytes.Repeat([]byte{byte(len(out) % 7)}, len(out)%13+1)...)
	}
	return out
}

// pigzFiles are the files of the test directory, by name. The compressed
// files come from the port: the cases that compress check those bytes.
type pigzFiles map[string][]byte

func pigzInputs(t *testing.T, port func(dir string, args ...string) []byte) pigzFiles {
	t.Helper()
	corpus := pigzCorpus()
	files := pigzFiles{
		"a":   corpus,
		"b":   pigzBinary(),
		"mid": corpus[:100000],
		"hi":  []byte("hello\n"),
		"x":   []byte("x exists\n"),
	}
	gen := t.TempDir()
	for name, data := range files {
		pigzWrite(t, filepath.Join(gen, name), data, 0o644)
	}
	g := port(gen, "-c", "-p", "4", "a")
	files["g.gz"] = g
	files["x.gz"] = port(gen, "-c", "hi")
	files["n.gz"] = port(gen, "-c", "-N", "hi")
	files["z.zz"] = port(gen, "-c", "-z", "-p", "4", "a")
	files["k.zip"] = port(gen, "-c", "-K", "-p", "4", "a")
	files["cm.gz"] = port(gen, "-c", "-C", "a note", "hi")
	files["pre.gz"] = port(gen, "-c", "mid")
	files["multi.gz"] = append(append([]byte{}, g...), g...)
	files["x.tgz"] = g
	files["junk.gz"] = append(append([]byte{}, g...), "junk"...)
	files["zjunk.zz"] = append(append([]byte{}, files["z.zz"]...), 'x')
	files["trunc.gz"] = g[:1000]
	files["plain.gz"] = []byte("not compressed at all\n")
	files["empty.gz"] = nil
	flip := func(data []byte, at int) []byte {
		out := append([]byte{}, data...)
		if at < 0 {
			at += len(out)
		}
		out[at] ^= 0xff
		return out
	}
	files["crc.gz"] = flip(g, -8)
	files["len.gz"] = flip(g, -4)
	files["ad.zz"] = flip(files["z.zz"], -1)
	files["bad.gz"] = flip(g, 30)
	for _, name := range []string{"lines.Z", "lines9.Z"} {
		data, err := os.ReadFile(filepath.Join("../../../examples/pigz/testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = data
	}
	files["flags.Z"] = append([]byte{}, files["lines.Z"]...)
	files["flags.Z"][2] = 0x70
	files["half.Z"] = files["lines.Z"][:len(files["lines.Z"])/2]
	files["zd.zip"] = pigzZip(t, corpus[:50000], zip.Deflate)
	files["zs.zip"] = pigzZip(t, []byte("stored\n"), zip.Store)
	files["subgz/x.gz"] = files["x.gz"]
	files["subgz/deeper/g.gz"] = g
	files["sub/y"] = corpus[:5000]
	files["sub/deeper/z"] = []byte("deeper\n")
	return files
}

// pigzZip is a zip file whose entry has a data descriptor, as zip -
// writes it.
func pigzZip(t *testing.T, data []byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "entry.txt", Method: method, Modified: pigzStamp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pigzWrite(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, pigzStamp, pigzStamp); err != nil {
		t.Fatal(err)
	}
}

// pigzSetup writes the test directory: the files, a hard link, a symbolic
// link, and an empty file.
func pigzSetup(t *testing.T, files pigzFiles) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		mode := os.FileMode(0o644)
		if name == "b" {
			mode = 0o640
		}
		pigzWrite(t, filepath.Join(dir, name), data, mode)
	}
	pigzWrite(t, filepath.Join(dir, "empty"), nil, 0o644)
	if err := os.Link(filepath.Join(dir, "hi"), filepath.Join(dir, "hard")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pigzRun runs a CLI with the case in a fresh test directory and records
// what it leaves.
func pigzRun(t *testing.T, cli func(args ...string) *exec.Cmd, files pigzFiles, c pigzCase) pigzRecord {
	t.Helper()
	dir := pigzSetup(t, files)
	cmd := cli(c.args...)
	cmd.Dir = dir
	env := []string{"TZ=UTC"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GZIP=") && !strings.HasPrefix(kv, "PIGZ=") && !strings.HasPrefix(kv, "TZ=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, c.env...)
	in := os.DevNull
	if c.stdin != "" {
		in = filepath.Join(dir, c.stdin)
	}
	stdin, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", c.name, &stderr)
	}
	return pigzRecord{Exit: cmd.ProcessState.ExitCode(), Stderr: stderr.String(), Digest: pigzDigest(t, dir, stdout.Bytes())}
}

// pigzDigest is a hash of the standard output and of each entry of dir.
func pigzDigest(t *testing.T, dir string, stdout []byte) string {
	t.Helper()
	h := sha256.New()
	h.Write(stdout)
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			lines = append(lines, rel+" -> "+target)
		case info.IsDir():
			lines = append(lines, rel+"/")
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			lines = append(lines, fmt.Sprintf("%s %o %d %d %d %x", rel, info.Mode().Perm(), info.Size(), info.ModTime().Unix(), pigzLinks(info), sum[:8]))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h.Write([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(h.Sum(nil)[:12])
}

const pigzGolden = "../../../examples/pigz/testdata/pigz-2.8.json"

// TestSelfHostExamplePigzCLI runs the CLI on x86-64 and arm64 and checks
// that each case gives what pigz 2.8 gave: the same exit code, the same
// messages, and the same bytes in each output file and on the standard
// output. testdata/pigz-2.8.json holds what pigz gave. When
// FERN_PIGZ_ORACLE names a pigz 2.8 binary, the test runs it as well. With
// FERN_PIGZ_UPDATE=1, it then writes the file again from that binary.
func TestSelfHostExamplePigzCLI(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/pigz/pigz.fern")
	if err != nil {
		t.Fatal(err)
	}
	golden := map[string]pigzRecord{}
	if data, err := os.ReadFile(pigzGolden); err == nil {
		if err := json.Unmarshal(data, &golden); err != nil {
			t.Fatal(err)
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
			files := pigzInputs(t, func(dir string, args ...string) []byte {
				cmd := port(args...)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "TZ=UTC")
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("pigz %s: %v", strings.Join(args, " "), err)
				}
				return out
			})
			update := map[string]pigzRecord{}
			for _, c := range pigzCases {
				got := pigzRun(t, port, files, c)
				want, ok := golden[c.name]
				if oracle != "" {
					want = pigzRun(t, func(args ...string) *exec.Cmd { return exec.Command(oracle, args...) }, files, c)
					update[c.name] = want
					ok = true
				}
				if !ok {
					t.Errorf("%s: no record of pigz 2.8 in %s", c.name, pigzGolden)
					continue
				}
				if got != want {
					t.Errorf("pigz %s (%s):\n  got  exit %d, stderr %q, digest %s\n  want exit %d, stderr %q, digest %s", strings.Join(c.args, " "), c.name, got.Exit, got.Stderr, got.Digest, want.Exit, want.Stderr, want.Digest)
				}
			}
			if oracle != "" && os.Getenv("FERN_PIGZ_UPDATE") == "1" && target == "x86-64-linux" {
				data, err := json.MarshalIndent(update, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(pigzGolden, append(data, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Run("level 11", func(t *testing.T) {
				got := pigzRun(t, port, files, pc("level 11", "-11", "-k", "hi"))
				if got.Exit != 22 || got.Stderr != "pigz: abort: level 11 (zopfli) is not available in this port\n" {
					t.Errorf("pigz -11: exit %d, stderr %q", got.Exit, got.Stderr)
				}
			})
		})
	}
}

// pigzLinks is the link count of a file.
func pigzLinks(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// pigzNamed gives the binary the name pigz, which its messages print.
func pigzNamed(t *testing.T, bin string) string {
	t.Helper()
	named := filepath.Join(t.TempDir(), "pigz")
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(named, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return named
}
