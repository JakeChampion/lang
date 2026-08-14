package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testenv"
)

// The three checks here keep internal/testenv honest, in the way the
// capabilities completeness tests keep internal/caps and internal/platforms
// honest: the census is only worth having if a new variable cannot be added
// without landing in it, and the allowlist is only worth having if no site goes
// back to inheriting the ambient environment.

// envNameRe matches the families whose values change what the suites prove.
var envNameRe = regexp.MustCompile(`(?:FERN|RUN|DIFF_ORACLE)_[A-Z0-9_]+`)

// goEnvUseRe matches a Go-side read or per-test override of one of them.
var goEnvUseRe = regexp.MustCompile(`(?:os\.Getenv|os\.LookupEnv|t\.Setenv)\("((?:FERN|RUN|DIFF_ORACLE)_[A-Z0-9_]+)"`)

// fernEnvReadRe matches the compiler-side read: `env("NAME")` in a .fern source.
var fernEnvReadRe = regexp.MustCompile(`env\("((?:FERN|RUN|DIFF_ORACLE)_[A-Z0-9_]+)"\)`)

// inheritRe matches building a child environment by inheritance. exec.Cmd's own
// Environ() is the process environment plus PWD, so it leaks the same way.
// AMBIENT-OK: a pattern, not a child environment.
var inheritRe = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.Environ\(\)`)

// ambientOKRe acknowledges a use of the ambient environment that is not a child
// environment at all. The reason text is required.
var ambientOKRe = regexp.MustCompile(`AMBIENT-OK:\s*\S`)

// goTrees hold the Go code whose env reads must be classified. internal/testenv
// is excluded: its own tests deliberately read names nothing has classified, to
// prove that such a name does not reach a child.
var goTrees = []string{"internal", "cmd", "tools"}

// fernTrees hold the .fern sources the compiler and stdlib are built from. A
// test program's `env("FERN_E2E_VAR")` is test data, not a compiler knob, and
// lives outside these.
var fernTrees = []string{filepath.Join("examples", "self_host"), filepath.Join("internal", "stdlib")}

// ciTrees hold everything that can put a variable into a CI job's environment.
var ciTrees = []string{".github", "scripts"}

const testenvPkg = "internal/testenv"

func TestEnvCensusIsComplete(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	// sites: names whose use requires a classification. mentions: every name that
	// appears at all, which is what makes a dead census row visible.
	sites := map[string]string{}
	mentions := map[string]bool{}
	note := func(name, where string) {
		if _, ok := sites[name]; !ok {
			sites[name] = where
		}
	}

	for _, tree := range append(append([]string{}, goTrees...), append(fernTrees, ciTrees...)...) {
		walkFiles(t, root, tree, "", func(rel, text string) {
			for _, m := range envNameRe.FindAllString(text, -1) {
				mentions[m] = true
			}
		})
	}

	for _, tree := range goTrees {
		walkFiles(t, root, tree, ".go", func(rel, text string) {
			if strings.HasPrefix(filepath.ToSlash(rel), testenvPkg+"/") {
				return
			}
			for _, m := range goEnvUseRe.FindAllStringSubmatch(text, -1) {
				note(m[1], rel)
			}
		})
	}
	for _, tree := range fernTrees {
		walkFiles(t, root, tree, ".fern", func(rel, text string) {
			for _, m := range fernEnvReadRe.FindAllStringSubmatch(text, -1) {
				note(m[1], rel)
			}
		})
	}
	for _, tree := range ciTrees {
		walkFiles(t, root, tree, "", func(rel, text string) {
			for _, m := range envNameRe.FindAllString(text, -1) {
				note(m, rel)
			}
		})
	}

	for _, name := range sortedKeys(sites) {
		if testenv.Lookup(name) == nil {
			t.Errorf("%s (%s) changes what a run does but is not in testenv.Vars. "+
				"Classify it: Semantic if an ambient value can change what a compile emits, what an "+
				"emitted program does, or which number a gate compares against; Lane if a CI lane sets "+
				"it to select coverage or name a tool.", name, sites[name])
		}
	}

	// The other direction: a census entry nothing mentions any more is a dead row
	// that makes the list look more complete than it is.
	for _, v := range testenv.Vars {
		if !mentions[v.Name] {
			t.Errorf("%s is censused but nothing in the tree mentions it any more; delete the row", v.Name)
		}
	}
}

// A Semantic variable is classified on the promise that no lane sets it
// process-wide — that promise is what lets TestMain refuse to run when one is
// exported. A workflow that starts setting one must reclassify it, not discover
// this in a red suite.
func TestNoSemanticVariableIsSetByCI(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	semantic := map[string]bool{}
	for _, v := range testenv.Vars {
		if v.Class == testenv.Semantic {
			semantic[v.Name] = true
		}
	}
	walkFiles(t, root, ".github", "", func(rel, text string) {
		for i, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, name := range envNameRe.FindAllString(line, -1) {
				if !semantic[name] {
					continue
				}
				if strings.HasPrefix(trimmed, name+":") || strings.Contains(line, name+"=") {
					t.Errorf("%s:%d assigns %s, which testenv.Vars classes Semantic (ambient-forbidden). "+
						"Either drop the assignment or reclassify it as Lane.", rel, i+1, name)
				}
			}
		}
	})
}

// The mechanical half of #6833: a child environment must be constructed, not
// inherited. A site that needs the ambient environment for something that is not
// a child environment says so with an `AMBIENT-OK: <reason>` comment.
func TestNoTestInheritsTheAmbientEnvironment(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	for _, tree := range goTrees {
		walkFiles(t, root, tree, ".go", func(rel, text string) {
			if strings.HasPrefix(filepath.ToSlash(rel), testenvPkg+"/") {
				return
			}
			lines := strings.Split(text, "\n")
			for i, line := range lines {
				if !inheritRe.MatchString(line) {
					continue
				}
				if ambientOKRe.MatchString(line) || (i > 0 && ambientOKRe.MatchString(lines[i-1])) {
					continue
				}
				t.Errorf("%s:%d builds an environment from the ambient one. Use testenv.Clean / "+
					"testenv.With so the child holds exactly what the test names, or mark the line "+
					"`// AMBIENT-OK: <reason>` if it is not a child environment.", rel, i+1)
			}
		})
	}
}

// walkFiles calls fn(relPath, contents) for every file under root/tree with the
// given extension ("" for any).
func walkFiles(t *testing.T, root, tree, ext string, fn func(rel, text string)) {
	t.Helper()
	dir := filepath.Join(root, tree)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if ext != "" && filepath.Ext(path) != ext {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		fn(rel, string(src))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", tree, err)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
