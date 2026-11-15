package e2ecompiler

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostExampleGitCommitGraph runs random log commands with git and
// with the port in random histories with a commit graph: one file with
// the changed-path Bloom filters, a chain of two files, and one file with
// the generation numbers of version 1. git walks with the generation
// numbers, and the port must show the same commits in the same order.
// Then the test changes a date in the commit graph. git orders the
// commits by that date, so the port must too, and with core.commitGraph
// false both read the dates of the commits.
func TestSelfHostExampleGitCommitGraph(t *testing.T) {
	oracle := gitOracle(t)
	cli := buildSelfHostCLI(t)
	for i, run := range gitRuns() {
		port := gitPort(t, cli, run.target)
		t.Run(run.target+"-"+run.format, func(t *testing.T) {
			for k, kind := range []string{"bloom", "chain", "v1"} {
				dir := t.TempDir()
				gitRandomHistory(t, oracle, dir, int64(100+10*i+k), 70, run)
				must := func(args ...string) string {
					t.Helper()
					out, stderr, code := oracle.run(t, dir, "", args...)
					if code != 0 {
						t.Fatalf("git %v: exit %d\n%s", args, code, stderr)
					}
					return out
				}
				switch kind {
				case "bloom":
					must("commit-graph", "write", "--reachable", "--changed-paths")
				case "chain":
					old := must("rev-list", "--all", "--skip=30")
					cmd := oracle.cmd("commit-graph", "write", "--split=no-merge", "--stdin-commits", "--changed-paths")
					cmd.Dir = dir
					cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
					cmd.Stdin = strings.NewReader(old)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git commit-graph write --stdin-commits: %v\n%s", err, out)
					}
					must("commit-graph", "write", "--reachable", "--split=no-merge")
					chain, err := os.ReadFile(filepath.Join(dir, ".git/objects/info/commit-graphs/commit-graph-chain"))
					if err != nil || len(strings.Fields(string(chain))) != 2 {
						t.Fatalf("want a chain of two files: %q %v", chain, err)
					}
				case "v1":
					must("-c", "commitGraph.generationVersion=1", "commit-graph", "write", "--reachable", "--changed-paths")
				}
				refs := strings.Fields(must("for-each-ref", "--format=%(refname:short)"))
				rnd := rand.New(rand.NewSource(int64(k)))
				cases := gitRandomLog(rnd, refs, 60)
				for _, args := range [][]string{{"log", "--oneline", "--topo-order", "--all", "-n", "3"}, {"log", "--graph", "--oneline", "--all", "--first-parent"},
					{"log", "--oneline", "--", "dir/sub/e"}, {"log", "--oneline", "--", "dir/"}, {"log", "--oneline", "--all", "--", "a"},
					{"log", "--stat", "--oneline", "-n", "20", "--", "dir"}, {"rev-parse", "main..." + refs[len(refs)-1]}} {
					cases = append(cases, args)
				}
				for _, args := range cases {
					gitCompare(t, oracle, port, dir, "", args...)
				}
				if kind != "bloom" {
					continue
				}
				head := strings.TrimSpace(must("rev-parse", "HEAD~2"))
				setCommitGraphDate(t, filepath.Join(dir, ".git/objects/info/commit-graph"), head, 1900000000)
				for _, args := range [][]string{{"log", "--oneline", "--all"}, {"log", "--graph", "--oneline", "--all"}, {"log", "--oneline", "--date-order"}} {
					gitCompare(t, oracle, port, dir, "", args...)
				}
				must("config", "core.commitGraph", "false")
				for _, args := range [][]string{{"log", "--oneline", "--all"}, {"log", "--graph", "--oneline", "--all"}} {
					gitCompare(t, oracle, port, dir, "", args...)
				}
			}
		})
	}
}

// setCommitGraphDate changes the commit date of the commit `name` in the
// commit graph at path.
func setCommitGraphDate(t *testing.T, path, name string, date uint64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(name)
	if err != nil {
		t.Fatal(err)
	}
	width := len(raw)
	chunk := func(id string) int {
		for k := 0; k < int(b[6]); k++ {
			at := 8 + 12*k
			if string(b[at:at+4]) == id {
				return int(binary.BigEndian.Uint64(b[at+4:]))
			}
		}
		t.Fatalf("no chunk %s in the commit graph", id)
		return 0
	}
	fanout, names, data := chunk("OIDF"), chunk("OIDL"), chunk("CDAT")
	count := int(binary.BigEndian.Uint32(b[fanout+1020:]))
	for i := 0; i < count; i++ {
		if bytes.Equal(b[names+i*width:names+(i+1)*width], raw) {
			at := data + i*(width+16) + width + 8
			level := binary.BigEndian.Uint32(b[at:]) &^ 3
			binary.BigEndian.PutUint32(b[at:], level|uint32(date>>32)&3)
			binary.BigEndian.PutUint32(b[at+4:], uint32(date))
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("the commit graph has no commit %s", name)
}
