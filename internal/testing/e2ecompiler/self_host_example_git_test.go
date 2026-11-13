package e2ecompiler

import (
	"bytes"
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
				{"", []string{"hash-object", "a.txt"}},
				{"", []string{"hash-object", "a.txt", "sp ace.txt"}},
				{"", []string{"hash-object", "nosuchfile"}},
				{"", []string{"hash-object", "-t", "bogus", "a.txt"}},
				{"abc", []string{"hash-object", "--stdin"}},
				{"abc", []string{"hash-object", "--stdin", "a.txt"}},
			}
			for _, dir := range []string{top, filepath.Join(top, "d")} {
				for _, c := range cases {
					args := c.args
					if dir != top && c.args[0] == "hash-object" {
						args = nil
						for _, a := range c.args {
							if strings.HasSuffix(a, ".txt") || a == "nosuchfile" {
								a = "../" + a
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
