package e2ecompiler

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
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
			tag := func(name, kind, label, person string) gitValidationObject {
				body := "object " + name + "\ntype " + kind + "\ntag " + label + "\n"
				if person != "" {
					body += "tagger " + person
				}
				return gitValidationObject{"tag", body + "\nmessage\n"}
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
				{name: "valid-tag", objects: []gitValidationObject{blob, tag(blobName, "blob", "v1", who)}},
				{name: "tag-type-forward", objects: []gitValidationObject{tag(blobName, "tree", "v1", who), blob}},
				{name: "tag-type-backward", objects: []gitValidationObject{blob, tag(blobName, "tree", "v1", who)}},
				{name: "tag-missing-target", objects: []gitValidationObject{tag(strings.Repeat("f", run.digits), "blob", "v1", who)}},
				{name: "tag-missing-tagger", objects: []gitValidationObject{blob, tag(blobName, "blob", "v1", "")}},
				{name: "tag-bad-name", objects: []gitValidationObject{blob, tag(blobName, "blob", "bad..name", who)}},
				{name: "tag-extra-header", objects: []gitValidationObject{blob, {"tag", strings.Replace(tag(blobName, "blob", "v1", who).body, "\n\nmessage", "\nextra x\n\nmessage", 1)}}},
				{name: "tag-signature", objects: []gitValidationObject{blob, {"tag", strings.Replace(tag(blobName, "blob", "v1", who).body, "\n\nmessage", "\ngpgsig signature\n continued\n\nmessage", 1)}}},
				{name: "tag-signature-extra", objects: []gitValidationObject{blob, {"tag", strings.Replace(tag(blobName, "blob", "v1", who).body, "\n\nmessage", "\ngpgsig signature\n continued\nextra x\n\nmessage", 1)}}},

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
				for _, flag := range []string{"--fsck-objects", "--strict", "--strict=missingAuthor=ignore,missingCommitter=warn,gitmodulesUrl=warn,gitmodulesParse=error,gitattributesLineLength=warn", "--strict=extraHeaderEntry=error", "--fsck-objects=badTree=ignore", "--strict=badTree=ignore"} {
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

// gitLiteralDelta is a delta from base to result that inserts each byte
// of result. It does not depend on the choice of compression.
func gitLiteralDelta(base, result string) []byte {
	delta := binary.AppendUvarint(nil, uint64(len(base)))
	delta = binary.AppendUvarint(delta, uint64(len(result)))
	for at := 0; at < len(result); {
		end := min(at+127, len(result))
		delta = append(delta, byte(end-at))
		delta = append(delta, result[at:end]...)
		at = end
	}
	return delta
}

// gitPackEntry appends to data one object of a packfile: the header of
// kind and of the size of body, then prefix, then the zlib stream of body.
func gitPackEntry(t *testing.T, data []byte, kind int, prefix, body []byte) []byte {
	t.Helper()
	n := len(body)
	b := byte(kind<<4 | n&15)
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
	data = append(data, prefix...)
	var compressed bytes.Buffer
	w := zlib.NewWriter(&compressed)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return append(data, compressed.Bytes()...)
}

// gitRefDelta is a delta by name in a packfile: the base, and the object
// that the delta makes.
type gitRefDelta struct{ base, result gitValidationObject }

// gitRefDeltaPack is a packfile with the deltas by name and then the
// objects extra.
func gitRefDeltaPack(t *testing.T, format string, deltas []gitRefDelta, extra []gitValidationObject) []byte {
	t.Helper()
	data := binary.BigEndian.AppendUint32([]byte("PACK\x00\x00\x00\x02"), uint32(len(deltas)+len(extra)))
	for _, d := range deltas {
		raw, err := hex.DecodeString(gitValidationName(format, d.base))
		if err != nil {
			t.Fatal(err)
		}
		data = gitPackEntry(t, data, 7, raw, gitLiteralDelta(d.base.body, d.result.body))
	}
	for _, o := range extra {
		data = gitPackEntry(t, data, map[string]int{"commit": 1, "tree": 2, "blob": 3, "tag": 4}[o.kind], nil, []byte(o.body))
	}
	return append(data, gitValidationHash(format, data)...)
}

func gitValidationRefPack(t *testing.T, format string, base, result gitValidationObject, includeBase bool, extra []gitValidationObject) []byte {
	t.Helper()
	if includeBase {
		extra = append([]gitValidationObject{base}, extra...)
	}
	return gitRefDeltaPack(t, format, []gitRefDelta{{base, result}}, extra)
}

func TestSelfHostExampleGitThinValidation(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			empty := gitValidationName(run.format, gitValidationObject{"tree", ""})
			who := "A <a@example.com> 1700000000 +0000\n"
			valid := gitValidationObject{"commit", "tree " + empty + "\nauthor " + who + "committer " + who + "\nmessage\n"}
			bad := gitValidationObject{"commit", "not a commit\n"}
			blob := gitValidationObject{"blob", "hello\n"}
			target := gitValidationName(run.format, blob)
			tag := func(kind, label string) gitValidationObject {
				return gitValidationObject{"tag", "object " + target + "\ntype " + kind + "\ntag " + label + "\ntagger " + who + "\nmessage\n"}
			}
			for _, c := range []struct {
				name         string
				base, result gitValidationObject
				include      bool
				extra        []gitValidationObject
			}{
				{"old-bad-base", bad, valid, false, nil},
				{"good-base", valid, gitValidationObject{"commit", valid.body + "extra\n"}, false, nil},
				{"bad-result", valid, bad, false, nil},
				{"explicit-bad-base", bad, valid, true, nil},
				{"delta-type-order", tag("blob", "base"), tag("tree", "result"), true, []gitValidationObject{blob}},
			} {
				for _, flag := range []string{"--strict", "--fsck-objects"} {
					type outcome struct {
						out, stderr string
						code        int
						objects     string
						files       map[string]string
					}
					execute := func(tool gitTool) outcome {
						t.Helper()
						dir := t.TempDir()
						gitInit(t, oracle, dir, run)
						if _, err, code := oracle.run(t, dir, c.base.body, "hash-object", "-w", "--literally", "-t", c.base.kind, "--stdin"); code != 0 {
							t.Fatal(err)
						}
						raw := gitValidationRefPack(t, run.format, c.base, c.result, c.include, c.extra)
						out, stderr, code := tool.run(t, dir, string(raw), "index-pack", "--stdin", "--fix-thin", "--no-rev-index", flag)
						listing := ""
						if code == 0 {
							var status int
							var err string
							listing, err, status = oracle.run(t, dir, "", "cat-file", "--batch-all-objects", "--batch")
							if status != 0 {
								t.Fatalf("cat-file: %s", err)
							}
						}
						if code != 0 {
							for _, suffix := range []string{"pack", "idx", "rev", "keep"} {
								names, _ := filepath.Glob(filepath.Join(dir, ".git/objects/pack/*."+suffix))
								if len(names) != 0 {
									t.Fatalf("%s: semantic failure wrote final %s files", c.name, suffix)
								}
							}
						}
						return outcome{out, stderr, code, listing, gitPackFiles(t, dir)}
					}
					want := execute(oracle)
					got := execute(port)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("%s %s: got exit %d, stdout %q, stderr %q, objects %q. Want exit %d, stdout %q, stderr %q, objects %q", c.name, flag, got.code, got.out, got.stderr, got.objects, want.code, want.out, want.stderr, want.objects)
					}
				}
			}
		})
	}
}

// gitPackFiles is the bytes of each final file in the pack directory of the
// repository dir, by name. The temporary files of git are not in it.
func gitPackFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	found, _ := filepath.Glob(filepath.Join(dir, ".git/objects/pack/pack-*"))
	for _, f := range found {
		files[filepath.Base(f)] = hex.EncodeToString(fileBytes(t, f))
	}
	return files
}

// index-pack --fix-thin writes the packfile, the index and the reverse
// index of git: the bases that git adds, in its order, with its zlib
// streams at each level that core.compression and core.loosecompression
// give, and the messages of git for a level that is not one. One thin
// pack comes from pack-objects --thin. Others have deltas by name whose
// bases are deltas of the same packfile, where git adds a base only for a
// delta that the bases before it do not complete. A base that is in the
// packfile two times stops git.
func TestSelfHostExampleGitFixThin(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			must := func(dir, stdin string, args ...string) string {
				t.Helper()
				out, stderr, code := oracle.run(t, dir, stdin, args...)
				if code != 0 {
					t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
				}
				return out
			}
			src := t.TempDir()
			gitInit(t, oracle, src, run)
			var text, numbers strings.Builder
			for i := 0; i < 4000; i++ {
				fmt.Fprintf(&text, "line %d of the text with the word %d\n", i, i*i%97)
				fmt.Fprintf(&numbers, "%d\n", i)
			}
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			id := []string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}
			write("text.txt", text.String())
			write("numbers.txt", numbers.String())
			must(src, "", "add", "-A")
			must(src, "", append(id, "commit", "-q", "-m", "one")...)
			write("text.txt", strings.ReplaceAll(text.String(), "word 5", "word five"))
			write("numbers.txt", numbers.String()+"4000\n")
			write("new.txt", "new\n")
			must(src, "", "add", "-A")
			must(src, "", append(id, "commit", "-q", "-m", "two")...)
			fetched := must(src, "HEAD\n^HEAD~1\n", "pack-objects", "--thin", "--stdout", "--revs", "-q")
			older := must(src, "HEAD~1\n", "pack-objects", "--stdout", "--revs", "-q")

			type outcome struct {
				out, stderr string
				code        int
				files       map[string]string
			}
			// The repository gets the objects of the packfile `have` and the
			// objects `loose`.
			execute := func(tool gitTool, config, have string, loose []gitValidationObject, thin string) outcome {
				t.Helper()
				dir := t.TempDir()
				gitInit(t, oracle, dir, run)
				if have != "" {
					must(dir, have, "unpack-objects", "-q")
				}
				for _, o := range loose {
					must(dir, o.body, "hash-object", "-w", "-t", o.kind, "--stdin")
				}
				f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString(config); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
				out, stderr, code := tool.run(t, dir, thin, "index-pack", "--stdin", "--fix-thin")
				return outcome{out, stderr, code, gitPackFiles(t, dir)}
			}
			compare := func(name, config, have string, loose []gitValidationObject, thin string) {
				t.Helper()
				want := execute(oracle, config, have, loose, thin)
				got := execute(port, config, have, loose, thin)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: got exit %d, stdout %q, stderr %q, files %d\nwant exit %d, stdout %q, stderr %q, files %d", name, got.code, got.out, got.stderr, len(got.files), want.code, want.out, want.stderr, len(want.files))
					for file, w := range want.files {
						if got.files[file] != w {
							t.Errorf("  %s differs", file)
						}
					}
				}
			}
			for _, config := range []string{
				"",
				"[core]\n\tcompression = 0\n",
				"[core]\n\tcompression = 6\n",
				"[core]\n\tcompression = 9\n",
				"[core]\n\tcompression = -1\n",
				"[core]\n\tloosecompression = 3\n\tcompression = 9\n",
				"[core]\n\tcompression = 4\n\tcompression = 2\n",
				"[core]\n\tcompression = 12\n",
				"[core]\n\tloosecompression = -2\n",
				"[core]\n\tcompression = x\n",
			} {
				compare("pack-objects --thin with "+strconv.Quote(config), config, older, nil, fetched)
			}
			base := gitValidationObject{"blob", "hello\n"}
			middle := gitValidationObject{"blob", "hello again\n"}
			last := gitValidationObject{"blob", "hello at the end\n"}
			local := []gitValidationObject{base, middle}
			compare("the base of the second delta is the first delta", "", "", local, string(gitRefDeltaPack(t, run.format, []gitRefDelta{{base, middle}, {middle, last}}, nil)))
			compare("the base of the first delta is the second delta", "", "", local, string(gitRefDeltaPack(t, run.format, []gitRefDelta{{middle, last}, {base, middle}}, nil)))
			compare("two deltas with one base", "", "", local, string(gitRefDeltaPack(t, run.format, []gitRefDelta{{base, last}, {base, middle}}, nil)))
			compare("a base that is not in the repository", "", "", []gitValidationObject{middle}, string(gitRefDeltaPack(t, run.format, []gitRefDelta{{base, middle}, {middle, last}}, nil)))
			compare("a base that the packfile has two times", "", "", nil, string(gitRefDeltaPack(t, run.format, []gitRefDelta{{base, middle}}, []gitValidationObject{base, last, base})))
		})
	}
}
