package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitHistoryFixture makes a repository with a history: commits on two
// branches and a merge, checkouts back and forth that the reflog of HEAD
// records, a remote with a branch that main follows, a branch that
// follows a local branch, an annotated and a light tag, packed refs and a
// directory. It also makes a second repository, `conflict`, whose index
// has the stages 1 to 3 of a conflict. The dates are fixed, so each run
// gives the same names.
func gitHistoryFixture(t *testing.T, oracle gitTool, dir string, hash gitRun) (string, string) {
	t.Helper()
	when := 1700000000
	run := func(at, stdin string, args ...string) {
		t.Helper()
		cmd := oracle.cmd(args...)
		cmd.Dir = at
		cmd.Stdin = strings.NewReader(stdin)
		stamp := fmt.Sprintf("%d +0100", when)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C",
			"GIT_AUTHOR_NAME=A", "GIT_AUTHOR_EMAIL=a@example.com", "GIT_COMMITTER_NAME=C", "GIT_COMMITTER_EMAIL=c@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil && args[0] != "merge" {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(at, name, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(at, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(at, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(at string, args ...string) {
		t.Helper()
		when += 3600
		run(at, "", append([]string{"commit", "-q"}, args...)...)
	}
	f := "--object-format=" + hash.format
	upstream := filepath.Join(dir, "upstream")
	repo := filepath.Join(dir, "revs")
	conflict := filepath.Join(dir, "conflict")
	for _, d := range []string{upstream, repo, conflict} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	run(upstream, "", "init", "-q", "-b", "main", f, ".")
	write(upstream, "u", "u\n")
	run(upstream, "", "add", "u")
	commit(upstream, "-m", "upstream one")

	run(repo, "", "init", "-q", "-b", "main", f, ".")
	write(repo, "a", "1\n")
	run(repo, "", "add", "a")
	commit(repo, "-m", "first: fix the base")
	write(repo, "a", "2\n")
	commit(repo, "-am", "second line\nwith a body")
	run(repo, "", "checkout", "-q", "-b", "side")
	write(repo, "s", "s\n")
	run(repo, "", "add", "s")
	commit(repo, "-m", "side: add s")
	run(repo, "", "checkout", "-q", "main")
	write(repo, "a", "3\n")
	commit(repo, "-am", "third: fix again")
	run(repo, "", "checkout", "-q", "side")
	write(repo, "s", "t\n")
	commit(repo, "-am", "side two")
	run(repo, "", "checkout", "-q", "main")
	when += 3600
	run(repo, "", "merge", "-q", "--no-ff", "-m", "merge side", "side")
	run(repo, "", "tag", "-a", "v1", "-m", "tag v1", "HEAD~1")
	run(repo, "", "tag", "light", "HEAD^2")
	run(repo, "", "remote", "add", "origin", upstream)
	run(repo, "", "fetch", "-q", "origin")
	run(repo, "", "branch", "-q", "--set-upstream-to=origin/main", "main")
	run(repo, "", "config", "branch.side.remote", ".")
	run(repo, "", "config", "branch.side.merge", "refs/heads/main")
	run(repo, "", "branch", "-q", "nobase", "HEAD~2")
	run(repo, "", "branch", "-q", "--no-track", "other", "origin/main")
	run(repo, "", "checkout", "-q", "--detach", "HEAD~1")
	run(repo, "", "checkout", "-q", "main")
	write(repo, "sub/x", "x\n")
	run(repo, "", "add", "sub")
	commit(repo, "-m", "sub dir")
	run(repo, "", "pack-refs", "--all")

	run(conflict, "", "init", "-q", "-b", "main", f, ".")
	write(conflict, "f", "base\n")
	write(conflict, "k", "keep\n")
	run(conflict, "", "add", "f", "k")
	commit(conflict, "-m", "base")
	run(conflict, "", "checkout", "-q", "-b", "b")
	write(conflict, "f", "theirs\n")
	commit(conflict, "-am", "theirs")
	run(conflict, "", "checkout", "-q", "main")
	write(conflict, "f", "ours\n")
	commit(conflict, "-am", "ours")
	run(conflict, "", "merge", "-q", "b")
	return repo, conflict
}

// TestSelfHostExampleGitRevisions reads each form of a revision with git
// and with the port: the operators, the ranges and the shorthands for the
// parents, the reflog with `@{N}`, `@{-N}` and dates, the upstream, the
// searches of messages, and the paths of the index with their stages. A
// form that names nothing gives the messages of git, with the line that
// git prints for `^` or `~` after a tree.
func TestSelfHostExampleGitRevisions(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	cases := []string{
		"rev-parse HEAD:a^", "rev-parse HEAD^{tree}^", "rev-parse HEAD^{tree}~2", "rev-parse --verify HEAD^{tree}^",
		"rev-parse v1^", "rev-parse v1^0 v1^{} v1~1 v1^^ light^", "rev-parse HEAD^{tree}^{commit}", "rev-parse HEAD:a^{tree}",
		"rev-parse HEAD~1", "rev-parse HEAD^2", "rev-parse HEAD^3",
		"rev-parse main~1..side", "rev-parse main...side", "rev-parse side...main", "rev-parse nobase...side",
		"rev-parse ^HEAD", "rev-parse ^main~2", "rev-parse HEAD^@", "rev-parse HEAD~1^@", "rev-parse HEAD~1^!",
		"rev-parse HEAD~1^-", "rev-parse HEAD~1^-1", "rev-parse HEAD~1^-2", "rev-parse HEAD~1^-3", "rev-parse HEAD^!x",
		"rev-parse ..side", "rev-parse side..", "rev-parse ..", "rev-parse ...", "rev-parse HEAD..HEAD^{tree}", "rev-parse HEAD...HEAD^{tree}",
		"rev-parse --short HEAD~1..side", "rev-parse --short HEAD", "rev-parse --verify HEAD~1..side", "rev-parse --verify -q nosuch",
		"rev-parse -q --verify HEAD", "rev-parse --verify HEAD main",
		"rev-parse @", "rev-parse @^ @~2", "rev-parse @{0}", "rev-parse @{1}", "rev-parse @{2}", "rev-parse @{5}", "rev-parse @{99}",
		"rev-parse HEAD@{0} HEAD@{1} HEAD@{2} HEAD@{3}", "rev-parse HEAD@{20}", "rev-parse main@{0} main@{1}", "rev-parse side@{1}",
		"rev-parse side@{9}", "rev-parse nosuch@{1}", "rev-parse @{-1}", "rev-parse @{-2}", "rev-parse @{-3}", "rev-parse @{-99}",
		"rev-parse @{-1}^", "rev-parse @{-1}@{1}", "rev-parse @{u}", "rev-parse @{U}", "rev-parse @{upstream}", "rev-parse main@{u}",
		"rev-parse side@{u}", "rev-parse other@{u}", "rev-parse nosuch@{u}", "rev-parse origin/main", "rev-parse main@{u}~0",
		"rev-parse main@{1700000000}", "rev-parse main@{2023-11-15T03:00:00+0000}", "rev-parse HEAD@{2000-01-01T00:00:00+0000}", "rev-parse main@{2023-11-15T00:30:00+0000}", "rev-parse main@{bogus-date}",
		"rev-parse HEAD^{/fix}", "rev-parse HEAD^{/^side}", "rev-parse HEAD^{/body}", "rev-parse HEAD^{/nomatch}", "rev-parse HEAD^{/!-fix}",
		"rev-parse HEAD^{/!!x}", "rev-parse HEAD^{/!x}", "rev-parse HEAD^{/}", "rev-parse HEAD^{/fix}~1", "rev-parse v1^{/two}",
		"rev-parse :/fix", "rev-parse :/side", "rev-parse :/^merge", "rev-parse :/nomatch", "rev-parse :/!-fix",
		"rev-parse :a", "rev-parse :0:a", "rev-parse :1:a", "rev-parse :nosuch", "rev-parse :sub/x", "rev-parse :./a", "rev-parse HEAD:./a",
		"rev-parse HEAD:nosuch", "rev-parse HEAD:sub/nosuch", "rev-parse nosuch:a", "rev-parse a", "rev-parse a nosuch", "rev-parse -- a",
		"rev-parse HEAD -- a", "rev-parse nosuch -- a", "rev-parse HEAD --bogus", "rev-parse --bogus", "rev-parse x*",
		"rev-parse HEAD^^{tree}", "rev-parse HEAD^{commit}^{tree}", "rev-parse v1^{tag}^{}", "rev-parse v1^{object}", "rev-parse HEAD^{blob}",
		"cat-file -t HEAD^{tree}^", "cat-file -t main@{1}", "cat-file -p :a", "cat-file -t @{-1}", "cat-file -t :/fix",
		"ls-tree @{-1}", "ls-tree HEAD^{tree}^",
		"rev-parse HEAD:x", "rev-parse HEAD:./x", "rev-parse :./x", "rev-parse :x", "rev-parse HEAD:../a", "rev-parse x", "rev-parse ../a",
	}
	conflictCases := []string{
		"rev-parse :f", "rev-parse :1:f :2:f :3:f", "rev-parse :0:f", "rev-parse :0:k", "rev-parse :2:k", "rev-parse :4:f",
		"rev-parse :1:nosuch", "cat-file -p :3:f", "cat-file -p :0:f", "ls-tree :2:f",
	}
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			repo, conflict := gitHistoryFixture(t, oracle, t.TempDir(), run)
			compare := func(dir string, line string) {
				t.Helper()
				args := strings.Fields(line)
				wantOut, wantErr, wantCode := oracle.run(t, dir, "", args...)
				gotOut, gotErr, gotCode := port.run(t, dir, "", args...)
				if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
					t.Errorf("git %v in %s:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, filepath.Base(dir), gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
				}
			}
			for _, dir := range []string{repo, filepath.Join(repo, "sub")} {
				for _, c := range cases {
					compare(dir, c)
				}
			}
			for _, c := range conflictCases {
				compare(conflict, c)
			}
		})
	}
}
