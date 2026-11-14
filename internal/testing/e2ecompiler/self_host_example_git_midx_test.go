package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostExampleGitMultiPackIndex gives the port a store of two
// packfiles with a multi-pack index. Each command prints what git prints.
// Then the test changes the multi-pack index so that two blobs change
// places. git reads the objects through that index, so the port must also
// read them through it. With core.multiPackIndex false, git and the port
// read the index of each packfile.
func TestSelfHostExampleGitMultiPackIndex(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			top := t.TempDir()
			gitFixture(t, oracle, top, run)
			must := func(args ...string) string {
				t.Helper()
				out, stderr, code := oracle.run(t, top, "", args...)
				if code != 0 {
					t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
				}
				return strings.TrimSpace(out)
			}
			must("repack", "-a", "-d", "-q")
			if err := os.WriteFile(filepath.Join(top, "b.txt"), []byte("second\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			must("add", "b.txt")
			must("-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "-m", "second")
			must("repack", "-d", "-q")
			must("multi-pack-index", "write")
			packs, _ := filepath.Glob(filepath.Join(top, ".git/objects/pack/*.pack"))
			if len(packs) != 2 {
				t.Fatalf("want two packfiles, have %v", packs)
			}
			names := must("cat-file", "--batch-all-objects", "--batch-check=%(objectname)") + "\n"
			compare := func(stdin string, args ...string) {
				t.Helper()
				wantOut, wantErr, wantCode := oracle.run(t, top, stdin, args...)
				gotOut, gotErr, gotCode := port.run(t, top, stdin, args...)
				if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
					t.Errorf("git %v:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
				}
			}
			run := func() {
				t.Helper()
				compare(names, "cat-file", "--batch")
				compare("", "cat-file", "-p", "HEAD:a.txt")
				compare("", "log", "--oneline", "--name-status")
				compare("", "rev-parse", "--short", "HEAD", "HEAD:b.txt")
			}
			run()
			a, q := must("rev-parse", "HEAD:a.txt"), must("rev-parse", "HEAD:sp ace.txt")
			swapMultiPackIndex(t, filepath.Join(top, ".git/objects/pack/multi-pack-index"), a, q)
			if got := must("cat-file", "-p", "HEAD:a.txt"); got != "q" {
				t.Fatalf("git does not read the changed multi-pack index: %q", got)
			}
			run()
			must("config", "core.multiPackIndex", "false")
			if got := must("cat-file", "-p", "HEAD:a.txt"); got != "hello" {
				t.Fatalf("git reads the multi-pack index that the configuration turns off: %q", got)
			}
			run()
		})
	}
}

// swapMultiPackIndex changes the places of the objects a and b in the
// offset chunk of the multi-pack index at path.
func swapMultiPackIndex(t *testing.T, path, a, b string) {
	t.Helper()
	data := fileBytes(t, path)
	chunk := func(id string) int {
		for k := 0; k < int(data[6]); k++ {
			at := 12 + 12*k
			if string(data[at:at+4]) == id {
				return int(binary.BigEndian.Uint64(data[at+4:]))
			}
		}
		t.Fatalf("no chunk %s", id)
		return 0
	}
	fanout, lookup, offsets := chunk("OIDF"), chunk("OIDL"), chunk("OOFF")
	count := int(binary.BigEndian.Uint32(data[fanout+1020:]))
	place := func(name string) int {
		raw, err := hex.DecodeString(name)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < count; i++ {
			if bytes.Equal(data[lookup+i*len(raw):lookup+(i+1)*len(raw)], raw) {
				return offsets + 8*i
			}
		}
		t.Fatalf("no object %s in the multi-pack index", name)
		return 0
	}
	x, y := place(a), place(b)
	entry := append([]byte(nil), data[x:x+8]...)
	copy(data[x:x+8], data[y:y+8])
	copy(data[y:y+8], entry)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
