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

func TestSelfHostExampleGitUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/git/git_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// gitTool runs one implementation of git in a directory.
type gitTool struct {
	name string
	cmd  func(args ...string) *exec.Cmd
}

func (g gitTool) run(t *testing.T, dir, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := g.cmd(args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s did not start: %s", g.name, &stderr)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// gitOracle is the git binary. A missing oracle fails the test.
func gitOracle(t *testing.T) gitTool {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal("git is required: it is the oracle of the git example")
	}
	return gitTool{name: "git", cmd: func(args ...string) *exec.Cmd { return exec.Command(git, args...) }}
}

// gitPort builds the port for target and gives a tool that runs it.
func gitPort(t *testing.T, cli *selfHostCLI, target string) gitTool {
	t.Helper()
	src, err := filepath.Abs("../../../examples/git/git.fern")
	if err != nil {
		t.Fatal(err)
	}
	if target == "x86-64-linux" {
		bin := cli.x86Binary(t, src)
		return gitTool{name: "fern git", cmd: func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }}
	}
	_, qemu := arm64Tooling(t)
	bin := cli.arm64Binary(t, src)
	return gitTool{name: "fern git", cmd: func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }}
}

// gitFixture makes a repository with one commit, a tree in a tree, a path
// that git quotes, a branch, a light tag and a tag object. Each object is
// loose. It gives the names of the commit, of its tree and of one blob.
func gitFixture(t *testing.T, oracle gitTool, dir string) (commit, tree, blob string) {
	t.Helper()
	must := func(stdin string, args ...string) string {
		t.Helper()
		out, stderr, code := oracle.run(t, dir, stdin, args...)
		if code != 0 {
			t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
		}
		return strings.TrimSpace(out)
	}
	must("", "init", "-q", "-b", "main", ".")
	for name, text := range map[string]string{"a.txt": "hello\n", "d/e/f.bin": "x", "sp ace.txt": "q", "t\tb": "u", "d/caf\xc3\xa9": "e"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := []string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}
	must("", "add", "-A")
	must("", append(id, "commit", "-q", "-m", "first", "--date=2026-01-02T03:04:05Z")...)
	must("", append(id, "tag", "-a", "v1", "-m", "tag one")...)
	must("", "tag", "light")
	must("", "branch", "side")
	return must("", "rev-parse", "HEAD"), must("", "rev-parse", "HEAD^{tree}"), must("", "rev-parse", "HEAD:a.txt")
}

// TestSelfHostExampleGitAgainstGit runs each command with git and with the
// port in one repository, at the top and in a directory below it. The
// standard output, the standard error output and the exit status are those
// of git.
func TestSelfHostExampleGitAgainstGit(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := gitPort(t, cli, target)
			top := t.TempDir()
			commit, tree, blob := gitFixture(t, oracle, top)
			zero := strings.Repeat("0", 40)
			cases := []struct {
				stdin string
				args  []string
			}{
				{"", []string{"rev-parse", "HEAD"}},
				{"", []string{"rev-parse", "main", "v1", "light", "side", "heads/main", "refs/heads/main", "tags/v1"}},
				{"", []string{"rev-parse", "--git-dir"}},
				{"", []string{"rev-parse", "nosuch"}},
				{"", []string{"rev-parse", "HEAD", "nosuch", "main"}},
				{"", []string{"rev-parse", "--verify", "nosuch"}},
				{"", []string{"rev-parse", "--verify", "HEAD"}},
				{"", []string{"rev-parse", "--short", "HEAD"}},
				{"", []string{"rev-parse", "--short=10", "v1"}},
				{"", []string{"rev-parse", commit[:8]}},
				{"", []string{"rev-parse", strings.ToUpper(commit)}},
				{"", []string{"rev-parse", zero}},
				{"", []string{"cat-file", "-t", "HEAD"}},
				{"", []string{"cat-file", "-s", "HEAD"}},
				{"", []string{"cat-file", "-p", "HEAD"}},
				{"", []string{"cat-file", "-p", tree}},
				{"", []string{"cat-file", "-t", tree}},
				{"", []string{"cat-file", "-s", tree}},
				{"", []string{"cat-file", "-t", "v1"}},
				{"", []string{"cat-file", "-p", "v1"}},
				{"", []string{"cat-file", "-p", blob}},
				{"", []string{"cat-file", "-s", blob}},
				{"", []string{"cat-file", "-t", blob[:6]}},
				{"", []string{"cat-file", "-t", "nosuch"}},
				{"", []string{"cat-file", "-p", zero}},
				{"", []string{"cat-file", "-t", zero}},
				{"", []string{"cat-file", "-s", zero}},
				{"", []string{"cat-file", "-e", "HEAD"}},
				{"", []string{"cat-file", "-e", zero}},
				{"HEAD\nnosuch\n" + tree + "\n\nv1\n" + blob + "\n", []string{"cat-file", "--batch-check"}},
				{"HEAD\nnosuch\n" + blob + "\nv1", []string{"cat-file", "--batch"}},
				{"", []string{"cat-file", "--batch-check"}},
				{"", []string{"rev-parse", "HEAD^{tree}", "HEAD:", "HEAD:d", "HEAD:d/e/f.bin", "v1^{commit}", "v1^{}", "v1^{tag}", "v1^{tree}", "HEAD^0", "HEAD~0"}},
				{"", []string{"rev-parse", "HEAD^"}},
				{"", []string{"rev-parse", "HEAD~1"}},
				{"", []string{"rev-parse", "HEAD:nosuch"}},
				{"", []string{"rev-parse", "--verify", "HEAD:nosuch"}},
				{"", []string{"rev-parse", "HEAD^{bogus}"}},
				{"", []string{"rev-parse", "HEAD^{"}},
				{"", []string{"cat-file", "-p", "HEAD:a.txt"}},
				{"", []string{"cat-file", "-t", "v1^{tree}"}},
				{"", []string{"cat-file", "-p", "HEAD:nosuch"}},
				{"", []string{"cat-file", "-s", "HEAD:d"}},
				{"HEAD:a.txt\nHEAD^{tree}\nv1^{}\nHEAD:nosuch\nHEAD^\n", []string{"cat-file", "--batch-check"}},
				{"", []string{"ls-tree", "HEAD"}},
				{"", []string{"ls-tree", "-r", "HEAD"}},
				{"", []string{"ls-tree", "-t", "HEAD"}},
				{"", []string{"ls-tree", "-r", "-t", "HEAD"}},
				{"", []string{"ls-tree", "-d", "HEAD"}},
				{"", []string{"ls-tree", "-d", "-r", "HEAD"}},
				{"", []string{"ls-tree", "-d", "-t", "HEAD"}},
				{"", []string{"ls-tree", "-l", "HEAD"}},
				{"", []string{"ls-tree", "-r", "-t", "--long", "HEAD"}},
				{"", []string{"ls-tree", "--name-only", "HEAD"}},
				{"", []string{"ls-tree", "--name-only", "-r", "-t", "HEAD"}},
				{"", []string{"ls-tree", "--full-tree", "-r", "HEAD"}},
				{"", []string{"ls-tree", "v1"}},
				{"", []string{"ls-tree", "HEAD:d"}},
				{"", []string{"ls-tree", "HEAD^{tree}"}},
				{"", []string{"ls-tree", "nosuch"}},
				{"", []string{"ls-tree", "HEAD:nosuch"}},
				{"", []string{"ls-tree", "HEAD:a.txt"}},
				{"", []string{"hash-object", "-t", "bogus", "nosuchfile"}},
				{"x", []string{"hash-object", "-t", "bogus", "--stdin"}},
				{"", []string{"hash-object", "a.txt"}},
				{"", []string{"hash-object", "a.txt", "sp ace.txt"}},
				{"", []string{"hash-object", "nosuchfile"}},
				{"", []string{"hash-object", "-t", "bogus", "a.txt"}},
				{"abc", []string{"hash-object", "--stdin"}},
				{"abc", []string{"hash-object", "--stdin", "a.txt"}},
			}
			for _, dir := range []string{top, filepath.Join(top, "d"), filepath.Join(top, "d", "e")} {
				for _, c := range cases {
					args := c.args
					if dir != top && c.args[0] == "hash-object" {
						args = nil
						for _, a := range c.args {
							if strings.HasSuffix(a, ".txt") || a == "nosuchfile" {
								a = strings.Repeat("../", strings.Count(strings.TrimPrefix(dir, top), string(filepath.Separator))) + a
							}
							args = append(args, a)
						}
					}
					wantOut, wantErr, wantCode := oracle.run(t, dir, c.stdin, args...)
					gotOut, gotErr, gotCode := port.run(t, dir, c.stdin, args...)
					if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
						t.Errorf("git %v in %s:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, strings.TrimPrefix(dir, top), gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
					}
				}
			}
			// The port writes a loose object, and git reads it and finds the store sound.
			text := "written by the port\n" + strings.Repeat("line\n", 4000)
			name, stderr, code := port.run(t, top, text, "hash-object", "-w", "--stdin")
			if code != 0 {
				t.Fatalf("hash-object -w: exit %d\n%s", code, stderr)
			}
			if want, _, _ := oracle.run(t, top, text, "hash-object", "--stdin"); name != want {
				t.Errorf("hash-object -w gave %q, and git gives %q", name, want)
			}
			if back, _, _ := oracle.run(t, top, "", "cat-file", "-p", strings.TrimSpace(name)); back != text {
				t.Errorf("git reads %d bytes from the object that the port wrote, want %d", len(back), len(text))
			}
			if out, stderr, code := oracle.run(t, top, "", "fsck", "--strict", "--no-dangling"); code != 0 {
				t.Errorf("git fsck after the write: exit %d\n%s%s", code, out, stderr)
			}
			// The port reads the object that it wrote, and writes it one time only.
			if back, _, _ := port.run(t, top, "", "cat-file", "-p", strings.TrimSpace(name)); back != text {
				t.Errorf("the port reads %d bytes from its own object, want %d", len(back), len(text))
			}
			if again, _, code := port.run(t, top, text, "hash-object", "-w", "--stdin"); code != 0 || again != name {
				t.Errorf("the second write: exit %d, name %q", code, again)
			}
		})
	}
}

// gitPackedFixture makes a repository whose objects are in one packfile,
// with chains of deltas: each commit changes one more line of a file of
// 400 lines. The last commit is a merge of two parents. It gives the path of the index of the packfile.
func gitPackedFixture(t *testing.T, oracle gitTool, dir string) string {
	t.Helper()
	must := func(args ...string) string {
		t.Helper()
		out, stderr, code := oracle.run(t, dir, "", args...)
		if code != 0 {
			t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
		}
		return strings.TrimSpace(out)
	}
	must("init", "-q", "-b", "main", ".")
	id := []string{"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}
	for i := 1; i <= 9; i++ {
		var big strings.Builder
		for line := 1; line <= 400; line++ {
			if line <= i*40 && line%40 == 0 {
				fmt.Fprintf(&big, "changed at %d\n", line)
			} else {
				fmt.Fprintf(&big, "line %d of the file\n", line)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		must("add", "-A")
		must(append(id, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))...)
	}
	must("checkout", "-q", "-b", "side", "HEAD~2")
	if err := os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must("add", "-A")
	must(append(id, "commit", "-q", "-m", "on the side")...)
	must("checkout", "-q", "main")
	must(append(id, "merge", "-q", "--no-ff", "-m", "merge", "side")...)
	must(append(id, "tag", "-a", "v1", "-m", "tag one")...)
	must("gc", "-q", "--aggressive", "--prune=now")
	return gitPackIndex(t, dir)
}

func gitPackIndex(t *testing.T, dir string) string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.idx"))
	if err != nil || len(found) != 1 {
		t.Fatalf("the fixture has %d packfiles, want 1", len(found))
	}
	return found[0]
}

// TestSelfHostExampleGitPacked reads a repository whose objects are in a
// packfile: with deltas by offset, and then with deltas by name. Each
// command gives the output of git, and verify-pack -v gives each line of
// git for the packfile. A packfile with one changed byte is refused.
func TestSelfHostExampleGitPacked(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			port := gitPort(t, cli, target)
			top := t.TempDir()
			idx := gitPackedFixture(t, oracle, top)
			compare := func(stdin string, args ...string) string {
				t.Helper()
				wantOut, wantErr, wantCode := oracle.run(t, top, stdin, args...)
				gotOut, gotErr, gotCode := port.run(t, top, stdin, args...)
				if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
					t.Errorf("git %v:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
				}
				return wantOut
			}
			check := func(idx string) {
				t.Helper()
				listing := compare("", "verify-pack", "-v", idx)
				if !strings.Contains(listing, "chain length = 2:") {
					t.Errorf("the fixture has no chain of two deltas:\n%s", listing)
				}
				compare("", "verify-pack", idx)
				compare("", "verify-pack", "-v", strings.TrimSuffix(idx, ".idx")+".pack")
				var names strings.Builder
				for _, line := range strings.Split(listing, "\n") {
					if f := strings.Fields(line); len(f) >= 5 && len(f[0]) == 40 {
						names.WriteString(f[0] + "\n")
						compare("", "cat-file", "-t", f[0])
						compare("", "cat-file", "-p", f[0][:9])
					}
				}
				compare(names.String()+"nosuch\n", "cat-file", "--batch-check")
				compare(names.String(), "cat-file", "--batch")
				compare("", "rev-parse", "HEAD", "main", "v1", "tags/v1")
				compare("", "rev-parse", "--short", "HEAD")
				compare("", "cat-file", "-p", "HEAD")
				compare("", "cat-file", "-p", "v1")
				compare("", "rev-parse", "HEAD^", "HEAD^1", "HEAD^2", "HEAD~2", "HEAD^^", "HEAD^2~1", "HEAD^2^{tree}", "v1^{}", "v1~3", "HEAD^0", "HEAD~4:small.txt", "side^:big.txt")
				compare("", "rev-parse", "HEAD^3")
				compare("", "rev-parse", "HEAD~99")
				compare("", "cat-file", "-p", "HEAD^2")
				compare("", "cat-file", "-p", "HEAD~5:small.txt")
				compare("", "ls-tree", "-l", "HEAD")
				compare("", "ls-tree", "-r", "HEAD^2")
			}
			check(idx)
			if _, stderr, code := oracle.run(t, top, "", "-c", "repack.useDeltaBaseOffset=false", "repack", "-q", "-a", "-d", "-f", "--depth=10"); code != 0 {
				t.Fatalf("git repack: exit %d\n%s", code, stderr)
			}
			idx = gitPackIndex(t, top)
			check(idx)
			compare("", "verify-pack", "-v", "nosuch.idx")

			// One changed byte in the middle of the packfile: git and the port refuse it.
			packed := strings.TrimSuffix(idx, ".idx") + ".pack"
			data, err := os.ReadFile(packed)
			if err != nil {
				t.Fatal(err)
			}
			data[len(data)/2] ^= 0x40
			if err := os.Chmod(packed, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(packed, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, wantCode := oracle.run(t, top, "", "verify-pack", idx)
			if _, _, gotCode := port.run(t, top, "", "verify-pack", idx); wantCode == 0 || gotCode != wantCode {
				t.Errorf("verify-pack of a changed packfile: the port gives exit %d and git gives %d", gotCode, wantCode)
			}
		})
	}
}
