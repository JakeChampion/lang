package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gitIndexPackCases runs index-pack with git and with the port on the
// packfile `pack`: with each form of the index, from the standard input
// of a repository, and on copies of the packfile with one changed byte.
// The standard output, the messages, the exit status and each file that
// index-pack writes are those of git.
func gitIndexPackCases(t *testing.T, oracle, port gitTool, pack []byte, format string) {
	t.Helper()
	type outcome struct {
		out, stderr string
		code        int
		files       map[string]string
	}
	run := func(tool gitTool, data []byte, repo bool, stdin bool, args ...string) outcome {
		t.Helper()
		dir := t.TempDir()
		if repo {
			if _, stderr, code := oracle.run(t, dir, "", "init", "-q", "--object-format="+format, "."); code != 0 {
				t.Fatalf("git init: %s", stderr)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "p.pack"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		in := ""
		if stdin {
			in = string(data)
		}
		out, stderr, code := tool.run(t, dir, in, args...)
		files := map[string]string{}
		for _, pattern := range []string{"*.idx", "*.rev", "*.keep", ".git/objects/pack/*"} {
			found, _ := filepath.Glob(filepath.Join(dir, pattern))
			for _, f := range found {
				files[filepath.Base(f)] = fmt.Sprintf("%x", fileBytes(t, f))
			}
		}
		return outcome{out, stderr, code, files}
	}
	compare := func(data []byte, repo, stdin bool, args ...string) {
		t.Helper()
		want := run(oracle, data, repo, stdin, args...)
		// git cannot allocate the memory that a changed size asks for; the
		// count of bytes in its message depends on the host.
		if strings.Contains(want.stderr, "Out of memory") {
			return
		}
		got := run(port, data, repo, stdin, args...)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("git %v (in a repository: %v):\n got exit %d, stdout %q, stderr %q, files %d\nwant exit %d, stdout %q, stderr %q, files %d", args, repo, got.code, got.out, got.stderr, len(got.files), want.code, want.out, want.stderr, len(want.files))
			for name, w := range want.files {
				if got.files[name] != w {
					t.Errorf("  %s differs", name)
				}
			}
		}
	}
	f := "--object-format=" + format
	for _, args := range [][]string{
		{"index-pack", f, "--index-version=1", "p.pack"},
		{"index-pack", f, "--index-version=2,0x100", "-o", "q.idx", "p.pack"},
		{"index-pack", f, "--index-version=3", "p.pack"},
		{"index-pack", f, "--index-version=2,0x80000000", "p.pack"},
		{"index-pack", f, "--no-rev-index", "p.pack"},
		{"index-pack", "--object-format=bogus", "p.pack"},
		{"index-pack", f, "--fix-thin", "p.pack"},
		{"index-pack", f},
	} {
		compare(pack, false, false, args...)
	}
	for _, args := range [][]string{{"index-pack", "--stdin"}, {"index-pack", "--stdin", "--keep=why"}, {"index-pack", "--stdin", "--index-version=1"}, {"index-pack", "--stdin", f}} {
		compare(pack, true, true, args...)
	}
	compare(pack, false, true, "index-pack", "--stdin")
	// A changed byte in the header, in each of the first objects and then
	// across the packfile, a short packfile, and junk at its end.
	places := []int{}
	for at := 0; at < 64; at++ {
		places = append(places, at)
	}
	for at := 64; at < len(pack); at += len(pack)/40 + 1 {
		places = append(places, at)
	}
	for _, at := range places {
		for _, flip := range []byte{1, 0x40} {
			bad := append([]byte(nil), pack...)
			bad[at] ^= flip
			compare(bad, false, false, "index-pack", f, "p.pack")
		}
	}
	compare(pack[:len(pack)-1], false, false, "index-pack", f, "p.pack")
	compare(pack[:len(pack)/2], false, false, "index-pack", f, "p.pack")
	compare(append(append([]byte(nil), pack...), "junk"...), false, false, "index-pack", f, "p.pack")
}

// TestSelfHostExampleGitHashObject gives hash-object commits, trees and
// tags that are sound and that are not. The port checks their form as git
// does, with the messages of git, and writes the sound ones. git reads
// each object that the port writes, and finds the store sound.
func TestSelfHostExampleGitHashObject(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			top := t.TempDir()
			commit, tree, blob := gitFixture(t, oracle, top, run)
			body := func(name string) string {
				out, _, _ := oracle.run(t, top, "", "cat-file", "-p", name)
				return out
			}
			rawTree, _, _ := oracle.run(t, top, "", "cat-file", "tree", tree)
			id := strings.Repeat("0", len(commit))
			who := "A U <a@example.com> 1700000000 +0100"
			entry := func(mode, name, hash string) string {
				raw, _ := hex.DecodeString(hash)
				return mode + " " + name + "\x00" + string(raw)
			}
			cases := []struct{ kind, text string }{
				{"commit", body("HEAD")},
				{"commit", "tree " + tree + "\nparent " + commit + "\nauthor " + who + "\ncommitter " + who + "\n\nmessage\n"},
				{"commit", "tree " + tree + "\n\nno author\n"},
				{"commit", "tree " + tree[:10] + "\nauthor " + who + "\n"},
				{"commit", "tree " + tree + "\nauthor A U<a@example.com> 1 +0000\ncommitter " + who + "\n\n"},
				{"commit", "tree " + tree + "\nauthor <a@example.com> 1 +0000\n\n"},
				{"commit", "tree " + tree + "\nauthor A U <a@example.com> 01 +0000\n\n"},
				{"commit", "tree " + tree + "\nauthor A U <a@example.com> 99999999999999999999 +0000\n\n"},
				{"commit", "tree " + tree + "\nauthor A U <a@example.com> 1 +000\n\n"},
				{"commit", "tree " + tree + "\nauthor A U <a@example.com 1 +0000\n\n"},
				{"commit", "tree " + tree + "\nauthor " + who + "\nauthor " + who + "\ncommitter " + who + "\n\n"},
				{"commit", "tree " + tree + "\nauthor " + who + "\n\n"},
				{"commit", "tree " + tree + "\nauthor " + who + "\ncommitter " + who + "\n\nNUL\x00in body\n"},
				{"commit", "tree " + tree + "\nauthor " + who + "\x00\n"},
				{"commit", "no line end"},
				{"tag", body("v1")},
				{"tag", "object " + commit + "\ntype commit\ntag v2\n\nno tagger\n"},
				{"tag", "object " + commit + "\ntype bogus\ntag v2\ntagger " + who + "\n\n"},
				{"tag", "object " + commit + "\ntype commit\ntag bad..name\ntagger " + who + "\n\n"},
				{"tag", "object " + commit + "\ntype commit\ntagger " + who + "\n\n"},
				{"tag", "object " + commit + "\ntype commit\ntag v3\ntagger " + who + "\nextra header\n\nmessage\n"},
				{"tag", "type commit\n"},
				{"tree", rawTree},
				{"tree", entry("100644", "b", blob) + entry("100644", "a", blob)},
				{"tree", entry("100644", "a", blob) + entry("40000", "a", tree)},
				{"tree", entry("100664", "x", blob) + entry("100644", ".git", blob) + entry("40000", "..", tree)},
				{"tree", entry("0100644", "x", blob) + entry("120000", ".gitmodules", blob) + entry("100644", "y/z", id)},
				{"tree", entry("100644", "GIT~1", blob) + entry("100644", ".git\u200c", blob)},
				{"tree", entry("100644", "a", blob)[:10]},
				{"tree", entry("100644", "a", blob) + "1x"},
				{"tree", entry("", "a", blob)},
				{"tree", ""},
				{"blob", "any bytes\x00\xff"},
			}
			for _, c := range cases {
				for _, args := range [][]string{{"hash-object", "-t", c.kind, "--stdin"}, {"hash-object", "-t", c.kind, "--literally", "--stdin"}} {
					wantOut, wantErr, wantCode := oracle.run(t, top, c.text, args...)
					gotOut, gotErr, gotCode := port.run(t, top, c.text, args...)
					if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
						t.Errorf("git %v of %q:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, c.text, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
					}
				}
			}
			for _, args := range [][]string{{"hash-object", "-t", "bogus", "--literally", "--stdin"}, {"hash-object", "-t", "a b", "--literally", "--stdin"}, {"hash-object", "-tbogus", "--stdin"}} {
				wantOut, wantErr, wantCode := oracle.run(t, top, "x", args...)
				gotOut, gotErr, gotCode := port.run(t, top, "x", args...)
				if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
					t.Errorf("git %v:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
				}
			}
			// The port writes a sound commit, tree and tag, and git reads them.
			for _, c := range []struct{ kind, text string }{{"commit", cases[1].text}, {"tree", rawTree}, {"tag", "object " + commit + "\ntype commit\ntag v9\ntagger " + who + "\n\nnew tag\n"}} {
				name, stderr, code := port.run(t, top, c.text, "hash-object", "-w", "-t", c.kind, "--stdin")
				if code != 0 {
					t.Fatalf("hash-object -w -t %s: exit %d\n%s", c.kind, code, stderr)
				}
				name = strings.TrimSpace(name)
				if kind, _, _ := oracle.run(t, top, "", "cat-file", "-t", name); strings.TrimSpace(kind) != c.kind {
					t.Errorf("git reads the %s that the port wrote as %q", c.kind, kind)
				}
				if back, _, _ := oracle.run(t, top, "", "cat-file", c.kind, name); back != c.text {
					t.Errorf("git reads %q from the %s that the port wrote", back, c.kind)
				}
			}
			// A malformed object is not written.
			if _, _, code := port.run(t, top, "tree "+tree+"\n\n", "hash-object", "-w", "-t", "commit", "--stdin"); code != 128 {
				t.Errorf("hash-object -w of a malformed commit: exit %d, want 128", code)
			}
			if out, stderr, code := oracle.run(t, top, "", "fsck", "--strict", "--no-dangling"); code != 0 {
				t.Errorf("git fsck after the writes: exit %d\n%s%s", code, out, stderr)
			}
		})
	}
}

// gitSparsePack writes the objects of the repository in dir to one
// packfile whose offsets go past 2 GiB and past 4 GiB: the first objects
// are at its start, and a gap that the file system does not store is
// before each of the others. The index is of version 2, with the table of
// 8-byte offsets. The loose objects and the other packfiles are removed.
func gitSparsePack(t *testing.T, oracle gitTool, dir string) {
	t.Helper()
	listing, stderr, code := oracle.run(t, dir, "", "cat-file", "--batch-all-objects", "--batch")
	if code != 0 {
		t.Fatalf("git cat-file --batch-all-objects: %s", stderr)
	}
	type object struct {
		name       []byte
		kind, body string
		crc        uint32
		at         int64
	}
	var objs []object
	for rest := listing; len(rest) > 0; {
		nl := strings.IndexByte(rest, '\n')
		var name, kind string
		var size int
		fmt.Sscanf(rest[:nl], "%s %s %d", &name, &kind, &size)
		raw, _ := hex.DecodeString(name)
		objs = append(objs, object{name: raw, kind: kind, body: rest[nl+1 : nl+1+size]})
		rest = rest[nl+1+size+1:]
	}
	kinds := map[string]byte{"commit": 1, "tree": 2, "blob": 3, "tag": 4}
	f, err := os.Create(filepath.Join(dir, "big.pack"))
	if err != nil {
		t.Fatal(err)
	}
	header := []byte("PACK\x00\x00\x00\x02")
	header = binary.BigEndian.AppendUint32(header, uint32(len(objs)))
	at := int64(len(header))
	if _, err := f.Write(header); err != nil {
		t.Fatal(err)
	}
	gaps := []int64{0, 1<<31 + 100, 1<<32 + 5000}
	for k := range objs {
		if gap := gaps[k*len(gaps)/len(objs)]; gap > at {
			at = gap
		}
		o := &objs[k]
		size := len(o.body)
		c := kinds[o.kind]<<4 | byte(size&15)
		var entry []byte
		for size >>= 4; size > 0; size >>= 7 {
			entry = append(entry, c|0x80)
			c = byte(size & 127)
		}
		entry = append(entry, c)
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write([]byte(o.body))
		w.Close()
		entry = append(entry, z.Bytes()...)
		if _, err := f.WriteAt(entry, at); err != nil {
			t.Fatal(err)
		}
		o.at, o.crc = at, crc32.ChecksumIEEE(entry)
		at += int64(len(entry))
	}
	hashsz := len(objs[0].name)
	trailer := bytes.Repeat([]byte{7}, hashsz)
	if _, err := f.WriteAt(trailer, at); err != nil {
		t.Fatal(err)
	}
	f.Close()
	sort.Slice(objs, func(i, j int) bool { return bytes.Compare(objs[i].name, objs[j].name) < 0 })
	idx := []byte("\xfftOc\x00\x00\x00\x02")
	for b := 0; b < 256; b++ {
		n := sort.Search(len(objs), func(i int) bool { return int(objs[i].name[0]) > b })
		idx = binary.BigEndian.AppendUint32(idx, uint32(n))
	}
	for _, o := range objs {
		idx = append(idx, o.name...)
	}
	for _, o := range objs {
		idx = binary.BigEndian.AppendUint32(idx, o.crc)
	}
	var large []byte
	for _, o := range objs {
		if o.at >= 1<<31 {
			idx = binary.BigEndian.AppendUint32(idx, 0x80000000|uint32(len(large)/8))
			large = binary.BigEndian.AppendUint64(large, uint64(o.at))
		} else {
			idx = binary.BigEndian.AppendUint32(idx, uint32(o.at))
		}
	}
	idx = append(append(idx, large...), trailer...)
	if hashsz == 32 {
		sum := sha256.Sum256(idx)
		idx = append(idx, sum[:]...)
	} else {
		sum := sha1.Sum(idx)
		idx = append(idx, sum[:]...)
	}
	objects := filepath.Join(dir, ".git", "objects")
	entries, _ := os.ReadDir(objects)
	for _, e := range entries {
		if len(e.Name()) == 2 || e.Name() == "pack" {
			os.RemoveAll(filepath.Join(objects, e.Name()))
		}
	}
	if err := os.MkdirAll(filepath.Join(objects, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "big.pack"), filepath.Join(objects, "pack", "pack-big.pack")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "pack", "pack-big.idx"), idx, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSelfHostExampleGitLargePack reads a packfile of more than 4 GiB,
// with objects past 2 GiB and past 4 GiB in the table of 8-byte offsets of
// its index. The packfile is sparse, so it takes little disk. Each command
// gives the output of git.
func TestSelfHostExampleGitLargePack(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			top := t.TempDir()
			gitFixture(t, oracle, top, run)
			gitSparsePack(t, oracle, top)
			if info, err := os.Stat(filepath.Join(top, ".git", "objects", "pack", "pack-big.pack")); err != nil || info.Size() < 1<<32 {
				t.Fatalf("the packfile is not past 4 GiB: %v", err)
			}
			for _, c := range []struct {
				stdin string
				args  []string
			}{
				{"", []string{"rev-parse", "HEAD", "v1", "HEAD^{tree}", "HEAD:a.txt", "HEAD:d/e/f.bin"}},
				{"", []string{"rev-parse", "--short", "HEAD"}},
				{"", []string{"cat-file", "-p", "HEAD"}},
				{"", []string{"cat-file", "-p", "v1"}},
				{"", []string{"cat-file", "-t", "HEAD:d"}},
				{"", []string{"cat-file", "-s", "HEAD:a.txt"}},
				{"HEAD\nv1\nHEAD:a.txt\nHEAD:d/e/f.bin\nHEAD^{tree}\nlight\n", []string{"cat-file", "--batch-check"}},
				{"HEAD\nv1\nHEAD:a.txt\nHEAD:d\n", []string{"cat-file", "--batch"}},
				{"", []string{"ls-tree", "-r", "-l", "HEAD"}},
				{"", []string{"log"}},
				{"", []string{"log", "--format=%H %T %an %s"}},
			} {
				wantOut, wantErr, wantCode := oracle.run(t, top, c.stdin, c.args...)
				gotOut, gotErr, gotCode := port.run(t, top, c.stdin, c.args...)
				if wantCode != 0 || gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
					t.Errorf("git %v:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", c.args, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
				}
			}
		})
	}
}
