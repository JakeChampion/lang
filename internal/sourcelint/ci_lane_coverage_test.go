package sourcelint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The inverse of tools/testname_gate.sh, and the gap that gate could not see.
//
// testname_gate asks "does every selector a lane names resolve to a test?".
// This file asks the other half: "is every test selected by some lane?". The
// two questions are inverses and only one of them was ever asked, which is how
// four independent coverage holes existed at once — internal/e2eharness's 20
// tests in no lane (#8470), seven internal/e2eselfhost tests matching no shard
// selector (#8471), and two more one axis over (#8472, #8474).
//
// Every meta-gate in the repo checked that a selector resolves. None checked
// that a test is selected.

// laneFile is a workflow filename under .github/workflows.
type laneFile = string

// selectorLanes maps a package to the lane files whose test-name selectors
// reach it. Only packages OUTSIDE the unit lane's derived set need an entry:
// everything the unit lane runs is selected wholesale.
//
// The patterns themselves are NOT written here — they are read out of the
// named files, so a lane that changes its `-run` cannot leave a stale copy
// behind in this test. What lives here is the mapping from a lane to the
// package it points at, which the YAML expresses only as a shell argument.
var selectorLanes = map[string][]laneFile{
	"internal/e2e": {
		"test-e2e-arm64.yml",
		"test-e2e-x86_64.yml",
		"test-e2e-wasm.yml",
		"test-e2e-differential.yml",
		"test-e2e-selfhost.yml",
		"macos.yml",
	},
	"internal/e2eselfhost": {
		"test-e2e-selfhost.yml",
		"macos.yml",
	},
}

// TestEveryGoTestIsSelectedByALane fails when a `func Test*` in the tree is
// run by no CI lane.
//
// A test nothing runs is worse than a missing test: it reads as coverage, it
// is maintained as coverage, and it reports nothing. The four holes this gate
// was written for had stood for months apiece behind a wall of green checks.
func TestEveryGoTestIsSelectedByALane(t *testing.T) {
	root := mustRepoRoot(t)
	tests := collectGoTests(t, root)
	if len(tests) == 0 {
		t.Fatal("found no test functions at all — refusing to pass vacuously")
	}
	lanes := buildLanes(t, root)

	claimed := map[string]bool{}
	for _, l := range lanes {
		for _, p := range l.pkgs {
			claimed[p] = true
		}
	}

	var orphans []goTest
	unclaimedPkgs := map[string]bool{}
	for _, tc := range tests {
		if !claimed[tc.pkg] {
			unclaimedPkgs[tc.pkg] = true
			orphans = append(orphans, tc)
			continue
		}
		selected := false
		for _, l := range lanes {
			if l.selects(tc) {
				selected = true
				break
			}
		}
		if !selected {
			orphans = append(orphans, tc)
		}
	}

	if len(orphans) > 0 {
		for _, pkg := range sortedKeys(unclaimedPkgs) {
			t.Errorf("package %s has tests and no lane runs it. Either drop it from "+
				"scripts/unit-test-packages' exclusions, or give it a workflow and add "+
				"that workflow to selectorLanes in this file.", pkg)
		}
		byPkg := map[string][]goTest{}
		for _, o := range orphans {
			byPkg[o.pkg] = append(byPkg[o.pkg], o)
		}
		for _, pkg := range sortedKeys(byPkg) {
			if unclaimedPkgs[pkg] {
				continue // already reported as a whole-package hole
			}
			for _, o := range byPkg[pkg] {
				t.Errorf("%s is selected by no lane (%s). The lanes that reach %s are %v — "+
					"give it a name one of them selects, or a job of its own.",
					o.name, o.pos, pkg, selectorLanes[pkg])
			}
		}
		t.Logf("%d of %d tests are selected by no lane", len(orphans), len(tests))
	}
}

// goTest is one top-level `func Test*` in the tree.
type goTest struct {
	pkg  string // package dir relative to the repo root, e.g. "internal/e2e"
	name string
	pos  string // "internal/e2e/arm64_test.go:42"
}

// lane is one CI job's selection: a set of packages plus what it runs in them.
type lane struct {
	name string
	pkgs []string
	// all lanes run every test in pkgs except those matching exclude.
	all     bool
	exclude *regexp.Regexp
	// name lanes run the tests their selectors match. Two kinds, because
	// lanes select two ways: a resolved `-run` regex, and a bare test name
	// classified the way tools/testname_gate.sh classifies one (terminated by
	// $, | or ) means exact; anything else is a prefix filter).
	patterns []*regexp.Regexp
	exact    map[string]bool
	prefixes []string
}

func (l lane) selects(tc goTest) bool {
	if !contains(l.pkgs, tc.pkg) {
		return false
	}
	if l.all {
		return l.exclude == nil || !l.exclude.MatchString(tc.name)
	}
	if l.exact[tc.name] {
		return true
	}
	for _, p := range l.prefixes {
		if strings.HasPrefix(tc.name, p) {
			return true
		}
	}
	for _, re := range l.patterns {
		if re.MatchString(tc.name) {
			return true
		}
	}
	return false
}

func buildLanes(t *testing.T, root string) []lane {
	t.Helper()
	lanes := []lane{
		unitLane(t, root),
		wholePackageLane(t, root, "test-fernsmith.yml", "./internal/fernsmith/...", "internal/fernsmith"),
		e2eCatchAllLane(t, root),
		selfHostShardLane(t, root),
	}
	for pkg, files := range selectorLanes {
		for _, f := range files {
			lanes = append(lanes, nameSelectorLane(t, root, f, pkg))
		}
	}
	return lanes
}

// unitLane is the lane that runs `$(scripts/unit-test-packages)` with no -run.
// Its package set is DERIVED by running that script, so this gate and the lane
// cannot disagree about what the unit lane covers.
func unitLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-units.yml"
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, "$(scripts/unit-test-packages)") {
		t.Fatalf("%s no longer runs $(scripts/unit-test-packages); this gate reads the "+
			"unit lane's package set from that script and cannot model the lane without it", file)
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if selectorFlagRe.MatchString(line) {
			t.Fatalf("%s selects tests by name (%q); this gate assumes the unit lane runs "+
				"EVERY test in every package the script prints", file, strings.TrimSpace(line))
		}
	}

	out, err := exec.Command("bash", filepath.Join(root, "scripts", "unit-test-packages")).Output()
	if err != nil {
		t.Fatalf("run scripts/unit-test-packages: %v", err)
	}
	var pkgs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p := strings.TrimPrefix(strings.TrimSpace(line), modulePath+"/"); p != "" {
			pkgs = append(pkgs, p)
		}
	}
	if len(pkgs) == 0 {
		t.Fatal("scripts/unit-test-packages printed nothing")
	}
	return lane{name: file, pkgs: pkgs, all: true}
}

// wholePackageLane models a lane that runs one package with no -run filter.
func wholePackageLane(t *testing.T, root, file, mustContain, pkg string) lane {
	t.Helper()
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, mustContain) {
		t.Fatalf("%s no longer runs %s", file, mustContain)
	}
	return lane{name: file, pkgs: []string{pkg}, all: true}
}

// e2eCatchAllLane models test-e2e-other.yml: it lists the whole package and
// removes the prefixes that have a workflow of their own, so anything
// uncategorised lands in one of its shards.
func e2eCatchAllLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-e2e-other.yml"
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, "go test -list '.' ./internal/e2e/") {
		t.Fatalf("%s no longer enumerates internal/e2e with `go test -list '.'`; without "+
			"that, selection is no longer by negation and a new uncategorised test would "+
			"run nowhere", file)
	}
	skip := envValue(t, src, "CATCH_ALL_SKIP")
	return lane{
		name:    file,
		pkgs:    []string{"internal/e2e"},
		all:     true,
		exclude: regexp.MustCompile(skip),
	}
}

// selfHostShardLane models the `test` shards of test-e2e-selfhost.yml. They
// list the whole internal/e2eselfhost binary and remove the names that have a
// job of their own (which this gate then re-covers through those jobs' own
// -test.run selectors) plus anything quarantined.
//
// QUARANTINED_TESTS is the one sanctioned way for a test to be selected by no
// lane. It is read from the workflow rather than written here, so the reason
// stays where the mandatory issue reference is, and it is empty today.
func selfHostShardLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-e2e-selfhost.yml"
	src := readWorkflow(t, root, file)
	// `^Test` and `.` both select the whole binary here — every Go test name
	// begins with Test — so accept either spelling. What this pin is for is
	// the narrower prefix (`^TestSelfHost`) that dropped seven tests (#8471);
	// TestSelfHostShardsSelectEveryTest pins the exact wording separately.
	if !strings.Contains(src, `-test.list '^Test'`) && !strings.Contains(src, `-test.list '.'`) {
		t.Fatalf("%s no longer lists the whole internal/e2eselfhost binary; a test that "+
			"does not follow the TestSelfHost naming convention would run nowhere (#8471)", file)
	}
	alt := strings.Join([]string{
		envValue(t, src, "ISOLATED_DRIVER_TESTS"),
		envValue(t, src, "QUARANTINED_TESTS"),
		envValue(t, src, "OWN_JOB_TESTS"),
	}, "|")
	if q := envValue(t, src, "QUARANTINED_TESTS"); q != "" {
		t.Logf("quarantined in %s and therefore running in no lane: %s", file, q)
	}
	return lane{
		name:    file + " (shards)",
		pkgs:    []string{"internal/e2eselfhost"},
		all:     true,
		exclude: regexp.MustCompile("^(" + alt + ")$"),
	}
}

// nameSelectorLane reads what a lane file selects, two ways.
//
// The `-run` / `-test.run` / `-test.list` REGEX is the accurate reading, and
// the only one that gets an alternation like `^Test(WASM|Wasm)` right. It is
// used whenever it resolves: a `${{ matrix.test }}` or a shell variable
// computed at run time does not, and is skipped rather than guessed — under-
// reading a selector can only make this gate stricter.
//
// The bare NAME reading, classified the way tools/testname_gate.sh classifies
// one, is what covers those: a matrix that expands into `-run` spells its
// names out as literals elsewhere in the same file. Whole-line comments are
// prose and select nothing.
func nameSelectorLane(t *testing.T, root, file, pkg string) lane {
	t.Helper()
	src := readWorkflow(t, root, file)
	exact := map[string]bool{}
	var prefixes []string
	var patterns []*regexp.Regexp
	seen := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range selectorFlagRe.FindAllStringSubmatch(line, -1) {
			if re := resolveSelector(src, m[1]); re != nil {
				patterns = append(patterns, re)
			}
		}
		for _, m := range testNameRe.FindAllString(line, -1) {
			if strings.ContainsAny(m[len(m)-1:], "$|)") {
				exact[m[:len(m)-1]] = true
			} else if !seen[m] {
				seen[m] = true
				prefixes = append(prefixes, m)
			}
		}
	}
	// An exact name is also a legitimate prefix of itself; drop the duplicate
	// so the prefix list only carries genuine prefix filters.
	var kept []string
	for _, p := range prefixes {
		if !exact[p] {
			kept = append(kept, p)
		}
	}
	return lane{name: file, pkgs: []string{pkg}, exact: exact, prefixes: kept, patterns: patterns}
}

// testNameRe matches a test name plus, when present, the character that
// terminates it — the same two shapes tools/testname_gate.sh reads.
var testNameRe = regexp.MustCompile(`\bTest[A-Za-z0-9_]+[$|)]?`)

// selectorFlagRe captures the argument of a test-selection flag.
var selectorFlagRe = regexp.MustCompile(`-(?:test\.)?(?:run|list)[ =]+('[^']*'|"[^"]*"|[^\s]+)`)

// resolveSelector turns one selector argument into a regexp, or nil when it
// cannot be resolved statically or is too broad to be a name filter.
func resolveSelector(src, arg string) *regexp.Regexp {
	arg = strings.Trim(arg, `'"`)
	// Shell variables holding a name alternation are workflow-level env.
	for _, m := range envRefRe.FindAllStringSubmatch(arg, -1) {
		if v, ok := lookupEnv(src, m[1]); ok {
			arg = strings.ReplaceAll(arg, m[0], v)
		}
	}
	arg = strings.ReplaceAll(arg, `\$`, `$`)
	// A run-time-computed filter (`$PAT`) or a matrix expansion resolves to
	// nothing readable here; the bare-name reading covers those.
	if strings.Contains(arg, "$") && !strings.HasSuffix(arg, "$") {
		return nil
	}
	if strings.Contains(arg, "{{") {
		return nil
	}
	re, err := regexp.Compile(arg)
	if err != nil {
		return nil
	}
	// A catch-all (`.`) is not a name filter; the lanes that select by
	// negation are modelled explicitly, with their exclusions.
	if re.MatchString("ZzNotATestNameAtAll") {
		return nil
	}
	return re
}

var envRefRe = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)

// lookupEnv reads a top-level `  KEY: "value"` entry, reporting whether it
// exists — envValue's non-fatal sibling.
func lookupEnv(src, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(?:"([^"]*)"|'([^']*)')\s*$`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

// envValue reads a top-level `  KEY: "value"` / `  KEY: 'value'` entry.
func envValue(t *testing.T, src, key string) string {
	t.Helper()
	// RE2 has no backreferences, so the two quote styles are separate arms.
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(?:"([^"]*)"|'([^']*)')\s*$`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no `%s:` entry found — this gate reads the lane's selection from it", key)
	}
	return m[1] + m[2]
}

// TestMacOSLaneFilterCoversWhatTheLaneBuilds pins macos.yml's `paths:` filter
// to the packages the job actually compiles.
//
// The lane is the only one that executes an arm64-darwin binary. A package
// missing from the filter is a package whose changes land with that check
// never firing, and the filter had drifted to name ten packages out of the 62
// the binary is built from — internal/platforms, the target-capability gate
// whose entire job is telling darwin from linux, among the omissions — while
// still listing internal/prelude, deleted long enough ago that CLAUDE.md
// records its absence (#8474).
func TestMacOSLaneFilterCoversWhatTheLaneBuilds(t *testing.T) {
	root := mustRepoRoot(t)
	src := readWorkflow(t, root, "macos.yml")
	on, ok := onBlock(src)
	if !ok {
		t.Fatal("macos.yml has no `on:` block")
	}

	// The pull_request and push filters must be the same list: a lane that
	// gates a PR on one set and main on another reports different things
	// about the same commit.
	blocks := pathsBlocks(on)
	if len(blocks) != 2 {
		t.Fatalf("expected exactly two `paths:` filters (pull_request + push), found %d", len(blocks))
	}
	if strings.Join(blocks[0], "\n") != strings.Join(blocks[1], "\n") {
		t.Errorf("macos.yml's pull_request and push path filters differ:\n  pull_request: %v\n  push:         %v",
			blocks[0], blocks[1])
	}
	entries := blocks[0]

	// Every entry must match something on disk. This is what catches an entry
	// left behind by a deletion, which matches nothing and says nothing.
	for _, e := range entries {
		pat := strings.TrimPrefix(e, "!")
		if !pathPatternMatchesSomething(t, root, pat) {
			t.Errorf("macos.yml path filter entry %q matches no file in the tree — "+
				"it has been silently selecting nothing", e)
		}
	}

	// Every package the lane compiles must be selected. The closure is the
	// driver plus the two test packages the job runs.
	closure := goListDeps(t, root, "./cmd/fern", "./internal/e2e", "./internal/e2eselfhost")
	for _, pkg := range closure {
		if !pathFilterSelects(entries, pkg+"/x.go") {
			t.Errorf("macos.yml does not run when %s changes, but the arm64-darwin "+
				"binary it builds and executes is compiled from it", pkg)
		}
	}
}

// pathsBlocks returns each `paths:` list in the `on:` block, as its quoted
// entries in order.
func pathsBlocks(on string) [][]string {
	var out [][]string
	var cur []string
	in := false
	entry := regexp.MustCompile(`^\s*-\s*"([^"]*)"\s*$`)
	for _, line := range strings.Split(on, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "paths:":
			if in {
				out = append(out, cur)
			}
			in, cur = true, nil
		case !in:
			// nothing
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			// blank lines and comments stay inside the list
		case entry.MatchString(line):
			cur = append(cur, entry.FindStringSubmatch(line)[1])
		default:
			out = append(out, cur)
			in, cur = false, nil
		}
	}
	if in {
		out = append(out, cur)
	}
	return out
}

// pathFilterSelects applies GitHub's `paths:` semantics to one path: the last
// matching pattern wins, and a `!` pattern excludes.
func pathFilterSelects(entries []string, path string) bool {
	selected := false
	for _, e := range entries {
		neg := strings.HasPrefix(e, "!")
		pat := strings.TrimPrefix(e, "!")
		if globMatches(pat, path) {
			selected = !neg
		}
	}
	return selected
}

// globMatches implements the subset of GitHub's filter-pattern syntax the
// macOS lane uses: `**` crosses `/`, `*` does not.
func globMatches(pattern, path string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		case pattern[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()).MatchString(path)
}

func pathPatternMatchesSomething(t *testing.T, root, pattern string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" || rel == "build" || rel == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if globMatches(pattern, rel) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return found
}

// goListDeps returns the repo-relative package dirs in the transitive closure
// of the named packages, tests included.
func goListDeps(t *testing.T, root string, pkgs ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-deps", "-test"}, pkgs...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps -test %v: %v", pkgs, err)
	}
	seen := map[string]bool{}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		p := strings.TrimSpace(line)
		if !strings.HasPrefix(p, modulePath+"/") {
			continue
		}
		p = strings.TrimPrefix(p, modulePath+"/")
		// `go list -test` names the synthesised test binaries too.
		if strings.ContainsAny(p, " [") || strings.HasSuffix(p, ".test") {
			continue
		}
		if !seen[p] {
			seen[p] = true
			dirs = append(dirs, p)
		}
	}
	if len(dirs) == 0 {
		t.Fatalf("go list -deps -test %v returned no in-module packages", pkgs)
	}
	sort.Strings(dirs)
	return dirs
}

const modulePath = "github.com/jakechampion/lang"

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	p, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return p
}

func readWorkflow(t *testing.T, root, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	return string(b)
}

// collectGoTests parses every _test.go the go tool would compile and returns
// its top-level test functions. Source parsing rather than `go test -list`
// because listing means building every test binary in the repo, internal/e2e
// and internal/e2eselfhost included — minutes of compile to answer a question
// the syntax already answers.
func collectGoTests(t *testing.T, root string) []goTest {
	t.Helper()
	var out []goTest
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// The go tool ignores these; so must the census, or a fixture
			// would be counted as an unrun test.
			if name == "testdata" || name == "vendor" ||
				(p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", p, perr)
		}
		pkg := filepath.ToSlash(filepath.Dir(filepath.ToSlash(mustRel(root, p))))
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name == nil {
				continue
			}
			n := fn.Name.Name
			if !isTestFuncName(n) {
				continue
			}
			out = append(out, goTest{
				pkg:  pkg,
				name: n,
				pos:  fmt.Sprintf("%s:%d", filepath.ToSlash(mustRel(root, p)), fset.Position(fn.Pos()).Line),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// isTestFuncName reports whether n is a name `go test` runs. TestMain is the
// package's entry point, not a test.
func isTestFuncName(n string) bool {
	if n == "TestMain" || !strings.HasPrefix(n, "Test") || len(n) == len("Test") {
		return false
	}
	r := n[len("Test")]
	return r == '_' || (r >= 'A' && r <= 'Z')
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
