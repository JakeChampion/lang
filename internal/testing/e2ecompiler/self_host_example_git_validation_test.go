package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type gitValidationObject struct{ kind, body string }

func gitValidationHash(format string, data []byte) []byte {
	if format == "sha256" {
		h := sha256.Sum256(data)
		return h[:]
	}
	h := sha1.Sum(data)
	return h[:]
}

func gitValidationName(format string, o gitValidationObject) string {
	return hex.EncodeToString(gitValidationHash(format, []byte(o.kind+" "+strconv.Itoa(len(o.body))+"\x00"+o.body)))
}

func gitValidationPack(t *testing.T, format string, objects []gitValidationObject) []byte {
	t.Helper()
	data := binary.BigEndian.AppendUint32([]byte("PACK\x00\x00\x00\x02"), uint32(len(objects)))
	for _, o := range objects {
		n := len(o.body)
		b := byte(map[string]int{"commit": 1, "tree": 2, "blob": 3, "tag": 4}[o.kind]<<4 | n&15)
		n >>= 4
		if n != 0 {
			b |= 128
		}
		data = append(data, b)
		for n != 0 {
			b = byte(n & 127)
			n >>= 7
			if n != 0 {
				b |= 128
			}
			data = append(data, b)
		}
		var compressed bytes.Buffer
		w := zlib.NewWriter(&compressed)
		if _, err := w.Write([]byte(o.body)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		data = append(data, compressed.Bytes()...)
	}
	return append(data, gitValidationHash(format, data)...)
}

// The reference supplies diagnostics, exit status, and every index byte.
func TestSelfHostExampleGitIndexValidation(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			blob := gitValidationObject{"blob", "hello\n"}
			blobName := gitValidationName(run.format, blob)
			raw, _ := hex.DecodeString(blobName)
			tree := gitValidationObject{"tree", "100644 x\x00" + string(raw)}
			treeName := gitValidationName(run.format, tree)
			empty := gitValidationName(run.format, gitValidationObject{"tree", ""})
			who := "A <a@example.com> 1700000000 +0000\n"
			commit := func(name string) gitValidationObject {
				return gitValidationObject{"commit", "tree " + name + "\nauthor " + who + "committer " + who + "\nmessage\n"}
			}
			type fixture struct {
				name           string
				objects, local []gitValidationObject
			}
			cases := []fixture{
				{name: "complete-forward", objects: []gitValidationObject{commit(treeName), tree, blob}},
				{name: "complete-backward", objects: []gitValidationObject{blob, tree, commit(treeName)}},
				{name: "missing-tree", objects: []gitValidationObject{commit(treeName)}},
				{name: "local-tree", objects: []gitValidationObject{commit(treeName)}, local: []gitValidationObject{tree, blob}},
				{name: "local-wrong-type", objects: []gitValidationObject{commit(blobName)}, local: []gitValidationObject{blob}},
				{name: "forward-wrong-type", objects: []gitValidationObject{commit(blobName), blob}},
				{name: "bad-commit", objects: []gitValidationObject{{"commit", "bad\n"}}},
				{name: "missing-people", objects: []gitValidationObject{{"commit", "tree " + empty + "\n\nmessage\n"}}},
				{name: "invalid-mode", objects: []gitValidationObject{blob, {"tree", "644 x\x00" + string(raw)}}},
				{name: "bad-tree", objects: []gitValidationObject{{"tree", "bad"}}},
				{name: "missing-gitlink", objects: []gitValidationObject{{"tree", "160000 sub\x00" + strings.Repeat("\xff", run.digits/2)}}},
			}
			for _, special := range []struct{ name, kind, body string }{
				{"modules-url", "gitmodules", "[submodule \"x\"]\nurl = -evil\n"},
				{"modules-parse", "gitmodules", "[submodule \"x\"\nurl = -evil\n"},
				{"attributes", "gitattributes", strings.Repeat("x", 2048) + "\n"},
			} {
				b := gitValidationObject{"blob", special.body}
				id, _ := hex.DecodeString(gitValidationName(run.format, b))
				tr := gitValidationObject{"tree", "100644 ." + special.kind + "\x00" + string(id)}
				cases = append(cases, fixture{name: special.name + "-before", objects: []gitValidationObject{b, tr}}, fixture{name: special.name + "-after", objects: []gitValidationObject{tr, b}}, fixture{name: special.name + "-missing", objects: []gitValidationObject{tr}})
			}
			type outcome struct {
				Out, Err string
				Code     int
				Files    map[string]string
			}
			execute := func(tool gitTool, c fixture, args []string, stdin bool) outcome {
				t.Helper()
				dir := t.TempDir()
				gitInit(t, oracle, dir, run)
				for _, o := range c.local {
					if _, err, code := oracle.run(t, dir, o.body, "hash-object", "-w", "--literally", "-t", o.kind, "--stdin"); code != 0 {
						t.Fatalf("local object: %s", err)
					}
				}
				data := gitValidationPack(t, run.format, c.objects)
				if err := os.WriteFile(filepath.Join(dir, "p.pack"), data, 0600); err != nil {
					t.Fatal(err)
				}
				in := ""
				if stdin {
					in = string(data)
				}
				out, stderr, code := tool.run(t, dir, in, args...)
				files := map[string]string{}
				for _, pattern := range []string{"*.idx", "*.rev", "*.keep", ".git/objects/pack/*"} {
					names, _ := filepath.Glob(filepath.Join(dir, pattern))
					for _, n := range names {
						// Git chooses a random name for its incomplete input spool.
						// Compare final pack artifacts, not that private temporary file.
						if strings.HasPrefix(filepath.Base(n), "tmp_pack_") {
							continue
						}
						rel, err := filepath.Rel(dir, n)
						if err != nil {
							t.Fatal(err)
						}
						files[rel] = string(fileBytes(t, n))
					}
				}
				return outcome{out, stderr, code, files}
			}
			for _, c := range cases {
				for _, flag := range []string{"--fsck-objects", "--strict", "--strict=missingAuthor=ignore,missingCommitter=warn,gitmodulesUrl=warn,gitmodulesParse=error,gitattributesLineLength=warn"} {
					for _, stdin := range []bool{false, true} {
						args := []string{"index-pack", "--no-rev-index", flag}
						if stdin {
							args = append(args, "--stdin", "--keep=validation")
						} else {
							args = append(args, "-o", "q.idx", "p.pack")
						}
						want := execute(oracle, c, args, stdin)
						got := execute(port, c, args, stdin)
						if !reflect.DeepEqual(got, want) {
							t.Errorf("%s stdin=%v %v:\n got exit %d, stdout %q, stderr %q, files %d\nwant exit %d, stdout %q, stderr %q, files %d", c.name, stdin, args, got.Code, got.Out, got.Err, len(got.Files), want.Code, want.Out, want.Err, len(want.Files))
						}
					}
				}
			}
			for _, flag := range []string{"--strict=missingAuthor", "--strict=doesNotExist=warn", "--strict=nulInHeader=warn", "--strict=unterminatedHeader=ignore", "--fsck-objects=missingAuthor=IGNORE"} {
				c := fixture{name: "invalid-policy", objects: []gitValidationObject{blob}}
				args := []string{"index-pack", flag, "p.pack"}
				want := execute(oracle, c, args, false)
				got := execute(port, c, args, false)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: got %+v, want %+v", flag, got, want)
				}
			}
		})
	}
}
