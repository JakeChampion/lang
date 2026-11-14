package e2ecompiler

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitLogCases are the log commands for the repository of
// gitHistoryFixture: the ranges, the paths with the simplification of the
// history, the filters, the sorts, the forms, the dates, the graph and the
// lists of the names that a commit changes.
var gitLogCases = []string{
	"log", "log --oneline", "log --oneline side", "log --oneline main side", "log --oneline side..main", "log --oneline main..side",
	"log --oneline main...side", "log --oneline ^side main", "log --oneline main --not side", "log --oneline HEAD~1^@",
	"log --oneline HEAD~1^!", "log --oneline HEAD~1^-", "log --oneline HEAD~1^-2",
	"log --oneline -- a", "log --oneline -- s", "log --oneline -- sub", "log --oneline -- nosuch", "log --oneline a", "log --oneline s sub",
	"log --oneline nosuch", "log --oneline main -- a", "log --oneline side -- a", "log --oneline -- *",
	"log --oneline --first-parent", "log --oneline --first-parent -- s", "log --oneline --topo-order", "log --oneline --date-order",
	"log --oneline --author-date-order", "log --oneline --reverse", "log --oneline --reverse -n 2", "log --oneline -n 2 --skip=1",
	"log --oneline --skip=3", "log --oneline -3", "log --oneline --max-count=2", "log --oneline --merges", "log --oneline --no-merges",
	"log --oneline --min-parents=2", "log --oneline --max-parents=0", "log --oneline --full-history -- a",
	"log --oneline --author=A", "log --oneline --author=nobody", "log --oneline --committer=C", "log --oneline --grep=fix",
	"log --oneline --grep=fix --grep=side", "log --oneline --grep=fix --grep=base --all-match", "log --oneline --grep=FIX -i",
	"log --oneline --grep=FIX", "log --oneline --grep=fix --invert-grep", "log --oneline -E --grep=fi(x|y)", "log --oneline -F --grep=fi.",
	"log --oneline --grep=body", "log --oneline --author=A --grep=side", "log --oneline --since=2023-11-15T04:00:00+0000",
	"log --oneline --until=2023-11-15T04:00:00+0000", "log --oneline --after=1700010000 --before=1700020000",
	"log --oneline --since=2023-11-15T04:00:00+0000 side..main", "log --oneline --since=3.years.ago", "log --oneline --until=yesterday",
	"log --oneline --all", "log --oneline --branches", "log --oneline --tags", "log --oneline --remotes",
	"log --oneline --parents", "log --parents -2", "log -2 --pretty=short", "log -2 --pretty=full", "log -2 --pretty=fuller",
	"log -2 --pretty=raw", "log -2 --pretty=reference", "log -2 --pretty=oneline", "log -2 --pretty",
	"log -2 --format=%h%n%b|%B|%f|%m|%x41|%x4|%e", "log -2 --format=%an%+s%-b% d|%D",
	"log -2 --format=%ai|%aI|%as|%ar|%aD|%ad|%at|%al|%ce|%cl|%cn|%ah|%cr", "log -2 --date=iso", "log -2 --date=iso-strict",
	"log -2 --date=rfc", "log -2 --date=short", "log -2 --date=raw", "log -2 --date=unix", "log -2 --date=relative",
	"log -2 --date=human", "log -2 --date=format:%Y/%m/%d|%H|%z|%a|%b", "log -2 --date=bogus", "log -2 --date=format",
	"log -2 --decorate", "log -2 --decorate --oneline", "log --format=%d", "log --oneline --name-only", "log --oneline --name-status",
	"log --name-only -3", "log --name-status --format=%s", "log --name-only --pretty=format:%s", "log --name-only -- a",
	"log --abbrev-commit -1", "log --no-abbrev-commit --oneline -1", "log HEAD:", "log v1", "log light", "log nosuch..HEAD",
	"log HEAD^{tree}^", "log -- a nosuch", "log a nosuch", "log --bogus", "log --reverse --graph",
	"log --graph --oneline", "log --graph", "log --graph --format=%h%n%s", "log --graph --format=%h", "log --graph --pretty=format:%h",
	"log --graph --oneline --all", "log --graph --oneline --name-status", "log --graph --name-only", "log --graph --oneline -- s",
	"log --graph --oneline -- a", "log --graph --oneline --first-parent", "log --graph --parents --oneline", "log --graph --oneline side..main",
	"log --graph --oneline --date-order", "log --graph --pretty=raw -2", "log --graph --format=",
}

// TestSelfHostExampleGitLog runs log with git and with the port in the
// repository of gitHistoryFixture, at its top and in a directory below it.
func TestSelfHostExampleGitLog(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for _, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			repo, _ := gitHistoryFixture(t, oracle, t.TempDir(), run)
			for _, dir := range []string{repo, filepath.Join(repo, "sub")} {
				for _, c := range gitLogCases {
					gitCompare(t, oracle, port, dir, "", strings.Fields(c)...)
				}
			}
		})
	}
}

// gitCompare runs one command with git and with the port in dir.
func gitCompare(t *testing.T, oracle, port gitTool, dir, stdin string, args ...string) {
	t.Helper()
	wantOut, wantErr, wantCode := oracle.run(t, dir, stdin, args...)
	gotOut, gotErr, gotCode := port.run(t, dir, stdin, args...)
	if gotOut != wantOut || gotErr != wantErr || gotCode != wantCode {
		t.Errorf("git %q in %s:\n got exit %d, stdout %q, stderr %q\nwant exit %d, stdout %q, stderr %q", args, filepath.Base(dir), gotCode, gotOut, gotErr, wantCode, wantOut, wantErr)
	}
}

// gitRandomHistory makes a repository with a random history from `seed`:
// branches, merges of two and of three parents, files in directories that
// change, go and move, dates that go back in time now and then, and tags.
func gitRandomHistory(t *testing.T, oracle gitTool, dir string, seed int64, n int, hash gitRun) {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed))
	when := int64(1700000000)
	stamp := func() string {
		when += []int64{60, 600, 3600, 86400, -7200, 0}[rnd.Intn(6)]
		return fmt.Sprintf("%d %s", when, []string{"+0000", "+0100", "-0500", "+0530"}[rnd.Intn(4)])
	}
	run := func(env []string, args ...string) (string, int) {
		cmd := oracle.cmd(args...)
		cmd.Dir = dir
		cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "LC_ALL=C"), env...)
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out)), code
	}
	must := func(env []string, args ...string) string {
		t.Helper()
		out, code := run(env, args...)
		if code != 0 {
			t.Fatalf("git %v: exit %d\n%s", args, code, out)
		}
		return out
	}
	names := []string{"Ann", "Bob", "Cy Dee"}
	who := func(name string) []string {
		email := strings.ToLower(strings.Fields(name)[0]) + "@example.com"
		return []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email, "GIT_AUTHOR_DATE=" + stamp(), "GIT_COMMITTER_DATE=" + stamp()}
	}
	words := []string{"fix", "add", "drop", "tidy", "merge", "base", "caf\u00e9", "tab\there", "\u4e2d\u6587\twide"}
	files := []string{"a", "b", "dir/c", "dir/d", "dir/sub/e", "other/f", "x.txt"}
	gitInit(t, oracle, dir, hash)
	must(who("Ann"), "commit", "-q", "--allow-empty", "-m", "root")
	branches := []string{"main"}
	for i := 0; i < n; i++ {
		cur := must(nil, "symbolic-ref", "--short", "HEAD")
		r := rnd.Float64()
		switch {
		case r < 0.15 && len(branches) < 5:
			b := fmt.Sprintf("b%d", i)
			must(nil, "checkout", "-q", "-b", b)
			branches = append(branches, b)
			continue
		case r < 0.30 && len(branches) > 1:
			must(nil, "checkout", "-q", branches[rnd.Intn(len(branches))])
			continue
		case r < 0.42 && len(branches) > 1:
			var others []string
			for _, b := range branches {
				if b != cur {
					others = append(others, b)
				}
			}
			rnd.Shuffle(len(others), func(a, b int) { others[a], others[b] = others[b], others[a] })
			picks := others[:1]
			if len(others) > 1 && rnd.Intn(3) == 0 {
				picks = others[:2]
			}
			if _, code := run(who("M"), append([]string{"merge", "-q", "--no-ff", "-m", "merge " + strings.Join(picks, " ") + " into " + cur}, picks...)...); code != 0 {
				run(nil, "merge", "--abort")
				run(nil, "reset", "-q", "--hard")
			}
			continue
		}
		if rnd.Intn(5) == 0 {
			src := files[rnd.Intn(len(files))]
			if data, err := os.ReadFile(filepath.Join(dir, src)); err == nil {
				dst := []string{"moved/" + filepath.Base(src), src + ".new", "dir/" + filepath.Base(src), fmt.Sprintf("z%d", i)}[rnd.Intn(4)]
				if _, err := os.Stat(filepath.Join(dir, dst)); err != nil {
					if rnd.Intn(2) == 0 {
						for k := 0; k < []int{1, 5, 40}[rnd.Intn(3)]; k++ {
							data = append(data, fmt.Sprintf("line %d of %d\n", k, i)...)
						}
					}
					os.MkdirAll(filepath.Dir(filepath.Join(dir, dst)), 0o755)
					os.WriteFile(filepath.Join(dir, dst), data, 0o644)
					os.Remove(filepath.Join(dir, src))
					files = append(files, dst)
				}
			}
		}
		for k := 0; k < []int{0, 1, 1, 2}[rnd.Intn(4)]; k++ {
			path := filepath.Join(dir, files[rnd.Intn(len(files))])
			os.MkdirAll(filepath.Dir(path), 0o755)
			if _, err := os.Stat(path); err == nil && rnd.Intn(7) == 0 {
				os.Remove(path)
				continue
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprint(f, strings.Repeat(fmt.Sprintf("%d %s\n", i, words[rnd.Intn(len(words))]), []int{1, 1, 3, 20}[rnd.Intn(4)]))
			f.Close()
		}
		must(nil, "add", "-A")
		msg := fmt.Sprintf("%s %d", words[rnd.Intn(len(words))], i)
		if rnd.Intn(2) == 0 {
			msg += fmt.Sprintf("\n\n%s body\t%d", words[rnd.Intn(len(words))], i)
		}
		cleanup := "--cleanup=strip"
		if i%7 == 0 {
			// Spaces at the ends of the lines, which the medium format drops.
			msg += "  \n\nend \t\n"
			cleanup = "--cleanup=verbatim"
		}
		must(who(names[rnd.Intn(len(names))]), "commit", "-q", "--allow-empty", cleanup, "-m", msg)
		if rnd.Intn(10) == 0 {
			must(nil, "tag", fmt.Sprintf("t%d", i))
		}
	}
	must(nil, "checkout", "-q", "main")
}

// TestSelfHostExampleGitLogRandom makes random histories and runs random
// log commands on them with git and with the port: each form, walk and
// range, with and without paths, and the graph.
func TestSelfHostExampleGitLogRandom(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	forms := [][]string{{"--oneline"}, {"--format=%h %p %s"}, {}, {"--pretty=raw"}, {"--parents", "--oneline"}, {"--name-status", "--oneline"},
		{"--name-only", "--format=%s"}, {"--graph", "--oneline"}, {"--graph"}, {"--graph", "--format=%h%n%s"}, {"--graph", "--name-status", "--oneline"},
		{"--graph", "--parents", "--format=%h %p"}}
	walks := [][]string{{}, {"--topo-order"}, {"--date-order"}, {"--first-parent"}, {"--reverse"}, {"--no-merges"}, {"--merges"}, {"-n", "5"},
		{"--author=Ann"}, {"--grep=fix"}, {"--since=1700100000"}, {"--until=1700100000"}, {"--author-date-order"}, {"--full-history"}}
	paths := []string{"a", "b", "dir", "dir/c", "dir/sub", "other", "x.txt", "dir/sub/e", "nosuch", "*.txt", "dir/*", "moved"}
	for i, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			for _, seed := range []int64{1, 2} {
				dir := t.TempDir()
				gitRandomHistory(t, oracle, dir, seed+int64(10*i), 70, run)
				list, _, _ := oracle.run(t, dir, "", "for-each-ref", "--format=%(refname:short)")
				refs := strings.Fields(list)
				rnd := rand.New(rand.NewSource(seed))
				for k := 0; k < 60; k++ {
					args := append(append([]string{"log"}, forms[rnd.Intn(len(forms))]...), walks[rnd.Intn(len(walks))]...)
					switch r := rnd.Float64(); {
					case r < 0.3 && len(refs) > 1:
						a, b := refs[rnd.Intn(len(refs))], refs[rnd.Intn(len(refs))]
						switch rnd.Intn(3) {
						case 0:
							args = append(args, a+".."+b)
						case 1:
							args = append(args, a+"..."+b)
						default:
							args = append(args, "^"+a, b)
						}
					case r < 0.5:
						args = append(args, refs[rnd.Intn(len(refs))])
					case r < 0.55:
						args = append(args, "--all")
					}
					if rnd.Intn(5) < 2 {
						args = append(args, "--", paths[rnd.Intn(len(paths))])
						if rnd.Intn(3) == 0 {
							args = append(args, paths[rnd.Intn(len(paths))])
						}
					}
					gitCompare(t, oracle, port, dir, "", args...)
				}
			}
		})
	}
}
