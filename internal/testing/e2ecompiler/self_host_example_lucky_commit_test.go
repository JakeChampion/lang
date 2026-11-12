package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostExampleLuckyCommitUnit(t *testing.T) {
	path, err := filepath.Abs("../../../examples/lucky_commit/lucky_test.fern")
	if err != nil {
		t.Fatal(err)
	}
	testCensusTargets(t, path)
}

// luckyRepo is a git repository with a fixed identity and no user or system
// configuration.
type luckyRepo struct {
	dir string
	env []string
}

func newLuckyRepo(t *testing.T, initArgs ...string) *luckyRepo {
	t.Helper()
	home := t.TempDir()
	r := &luckyRepo{dir: filepath.Join(home, "repo"), env: append(os.Environ(),
		"HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CEILING_DIRECTORIES="+home,
		"GIT_AUTHOR_NAME=Foo Bár", "GIT_AUTHOR_EMAIL=foo@example.com", "GIT_AUTHOR_DATE=1513980859 -0500",
		"GIT_COMMITTER_NAME=Baz Qux", "GIT_COMMITTER_EMAIL=baz@example.com", "GIT_COMMITTER_DATE=1513980898 -0500",
	)}
	if err := os.Mkdir(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.git(t, "", append([]string{"init", "-q"}, initArgs...)...)
	if err := os.WriteFile(filepath.Join(r.dir, "f"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, "", "add", "f")
	r.git(t, "", "commit", "-q", "-m", "First")
	if err := os.WriteFile(filepath.Join(r.dir, "f"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.git(t, "", "commit", "-q", "-a", "-m", "Do a thing\n\nMakes some changes to the foo feature")
	return r
}

func (r *luckyRepo) git(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env, cmd.Stdin = r.dir, r.env, strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (r *luckyRepo) rev(t *testing.T, name string) string {
	t.Helper()
	return strings.TrimSpace(r.git(t, "", "rev-parse", name))
}

// run runs the CLI in the repository and gives its stderr and exit code.
func (r *luckyRepo) run(t *testing.T, lucky func(args ...string) *exec.Cmd, args ...string) (string, int) {
	t.Helper()
	cmd := lucky(args...)
	cmd.Dir, cmd.Env = r.dir, r.env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("lucky_commit did not start: %s", &stderr)
	}
	return stderr.String(), cmd.ProcessState.ExitCode()
}

func matchesSpec(hash, spec string) bool {
	if len(spec) > len(hash) {
		return false
	}
	for i := 0; i < len(spec); i++ {
		if spec[i] != '_' && spec[i] != hash[i] {
			return false
		}
	}
	return true
}

// checkLucky runs the CLI with `spec` and checks that only whitespace and the
// hash changed.
func checkLucky(t *testing.T, r *luckyRepo, lucky func(args ...string) *exec.Cmd, spec string) {
	t.Helper()
	tree, parent := r.rev(t, "HEAD^{tree}"), r.rev(t, "HEAD^")
	message := r.git(t, "", "log", "-1", "--format=%B")
	if stderr, code := r.run(t, lucky, spec); code != 0 {
		t.Fatalf("lucky_commit %s: exit %d\n%s", spec, code, stderr)
	}
	head := r.rev(t, "HEAD")
	if !matchesSpec(head, spec) {
		t.Errorf("HEAD %s does not match %s", head, spec)
	}
	if got := r.rev(t, "HEAD^{tree}"); got != tree {
		t.Errorf("tree changed: %s, want %s", got, tree)
	}
	if got := r.rev(t, "HEAD^"); got != parent {
		t.Errorf("parent changed: %s, want %s", got, parent)
	}
	got := r.git(t, "", "log", "-1", "--format=%B")
	if strings.TrimRight(got, " \t\n") != strings.TrimRight(message, " \t\n") {
		t.Errorf("message changed beyond trailing whitespace: %q, want %q", got, message)
	}
	if reflog := r.git(t, "", "reflog", "-1", "--format=%gs"); strings.TrimSpace(reflog) != "amend with lucky_commit" {
		t.Errorf("reflog entry %q", reflog)
	}
	r.git(t, "", "fsck", "--strict")
}

func TestSelfHostExampleLuckyCommitGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the lucky_commit example")
	}
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../examples/lucky_commit/lucky_commit.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var lucky func(args ...string) *exec.Cmd
			if target == "x86-64-linux" {
				bin := cli.x86Binary(t, src)
				lucky = func(args ...string) *exec.Cmd { return runX86_64Bin(cli.runner, bin, args...) }
			} else {
				_, qemu := arm64Tooling(t)
				bin := cli.arm64Binary(t, src)
				lucky = func(args ...string) *exec.Cmd { return runArm64Bin(qemu, bin, args...) }
			}

			t.Run("unsigned", func(t *testing.T) {
				r := newLuckyRepo(t)
				checkLucky(t, r, lucky, "0000")
				size := len(r.git(t, "", "cat-file", "commit", "HEAD"))
				// A second run replaces the padding of the first.
				checkLucky(t, r, lucky, "abc_e")
				if got := len(r.git(t, "", "cat-file", "commit", "HEAD")); got != size {
					t.Errorf("the commit grew from %d to %d bytes", size, got)
				}
			})

			t.Run("signed", func(t *testing.T) {
				r := newLuckyRepo(t)
				object := r.git(t, "", "cat-file", "commit", "HEAD")
				split := strings.Index(object, "\n\n")
				signature := "gpgsig -----BEGIN PGP SIGNATURE-----\n \n " + strings.Repeat("A", 64) + "\n =AAAA\n -----END PGP SIGNATURE-----"
				signed := object[:split+1] + signature + object[split:]
				oid := strings.TrimSpace(r.git(t, signed, "hash-object", "-t", "commit", "-w", "--stdin"))
				r.git(t, "", "update-ref", "HEAD", oid)
				checkLucky(t, r, lucky, "000")
				got := r.git(t, "", "cat-file", "commit", "HEAD")
				// The signed payload is the message, so it is unchanged byte for byte.
				if !strings.HasSuffix(got, object[split:]) {
					t.Errorf("the signed message changed:\n%q", got)
				}
				end := strings.Index(got, "-----END PGP SIGNATURE-----")
				if end < 0 || strings.Trim(got[end+len("-----END PGP SIGNATURE-----"):len(got)-len(object[split:])], " \t") != "" {
					t.Errorf("the padding is not at the end of the signature:\n%q", got)
				}
			})

			t.Run("sha256", func(t *testing.T) {
				r := newLuckyRepo(t, "--object-format=sha256")
				checkLucky(t, r, lucky, "00")
				if head := r.rev(t, "HEAD"); len(head) != 64 {
					t.Errorf("HEAD %s is not a SHA-256 hash", head)
				}
			})

			t.Run("errors", func(t *testing.T) {
				r := newLuckyRepo(t)
				head := r.rev(t, "HEAD")
				for _, tc := range []struct {
					args []string
					want string
				}{
					{[]string{"abcdefg"}, "Error: hash spec contains invalid character 'g' (only hex characters and underscores are allowed)\n"},
					{[]string{strings.Repeat("0", 41)}, "Error: hash spec can't be longer than an actual hash\n"},
					{[]string{"00", "11"}, "Usage: lucky_commit [commit-hash-prefix]\n"},
					{[]string{"--help"}, "Usage: lucky_commit [commit-hash-prefix]\n"},
				} {
					stderr, code := r.run(t, lucky, tc.args...)
					if code != 1 || !strings.HasPrefix(stderr, tc.want) {
						t.Errorf("lucky_commit %v: exit %d, stderr %q; want exit 1, stderr %q", tc.args, code, stderr, tc.want)
					}
				}
				if got := r.rev(t, "HEAD"); got != head {
					t.Errorf("a failed run moved HEAD to %s", got)
				}
				outside := &luckyRepo{dir: filepath.Dir(r.dir), env: r.env}
				if stderr, code := outside.run(t, lucky); code != 1 || !strings.Contains(stderr, "lucky_commit: git cat-file finished with exit code 128") {
					t.Errorf("outside a repository: exit %d, stderr %q", code, stderr)
				}
			})
		})
	}
}
