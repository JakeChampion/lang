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
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type laneFile = string

var selectorLanes = map[string][]laneFile{
	"internal/testing/e2e": {
		"test-e2e-arm64.yml",
		"test-e2e-x86_64.yml",
		"test-e2e-wasm.yml",
		"test-e2e-differential.yml",
		"test-e2e-selfhost.yml",
		"preflight.yml",
		"macos.yml",
	},
	"internal/testing/e2ecompiler": {
		"test-e2e-selfhost.yml",
		"macos.yml",
	},
}

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
				continue
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

type goTest struct {
	pkg  string
	name string
	pos  string
}

type lane struct {
	name     string
	pkgs     []string
	all      bool
	exclude  *regexp.Regexp
	patterns []*regexp.Regexp
	exact    map[string]bool
	prefixes []string
}

func (l lane) selects(tc goTest) bool {
	if !slices.Contains(l.pkgs, tc.pkg) {
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
		wholePackageLane(t, root, "test-fernsmith.yml", "./internal/testing/fernsmith/...", "internal/testing/fernsmith"),
		e2eCatchAllLane(t, root),
		selfHostShardLane(t, root),
		coreutilsShardLane(t, root),
	}
	for pkg, files := range selectorLanes {
		for _, f := range files {
			lanes = append(lanes, nameSelectorLane(t, root, f, pkg))
		}
	}
	return lanes
}

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

	cmd := exec.Command("bash", filepath.Join(root, "scripts", "unit-test-packages"))
	cmd.Env = ciEnv()
	out, err := cmd.Output()
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

func wholePackageLane(t *testing.T, root, file, mustContain, pkg string) lane {
	t.Helper()
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, mustContain) {
		t.Fatalf("%s no longer runs %s", file, mustContain)
	}
	return lane{name: file, pkgs: []string{pkg}, all: true}
}

func e2eCatchAllLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-e2e-other.yml"
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, "go test -list '.' ./internal/testing/e2e/") {
		t.Fatalf("%s no longer enumerates internal/testing/e2e with `go test -list '.'`; without "+
			"that, selection is no longer by negation and a new uncategorised test would "+
			"run nowhere", file)
	}
	skip := envValue(t, src, "CATCH_ALL_SKIP")
	return lane{
		name:    file,
		pkgs:    []string{"internal/testing/e2e"},
		all:     true,
		exclude: regexp.MustCompile(skip),
	}
}

func selfHostShardLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-e2e-selfhost.yml"
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, `-test.list '^Test'`) && !strings.Contains(src, `-test.list '.'`) {
		t.Fatalf("%s no longer lists the whole internal/testing/e2ecompiler binary; a test that "+
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
		pkgs:    []string{"internal/testing/e2ecompiler"},
		all:     true,
		exclude: regexp.MustCompile("^(" + alt + ")$"),
	}
}

func coreutilsShardLane(t *testing.T, root string) lane {
	t.Helper()
	const file = "test-coreutils.yml"
	src := readWorkflow(t, root, file)
	if !strings.Contains(src, `go test -list '.' ./internal/testing/coreutils/`) {
		t.Fatalf("%s no longer lists the whole internal/testing/coreutils package; a test that does "+
			"not match a narrower pattern would run in no shard, and therefore nowhere", file)
	}
	if !strings.Contains(src, `find coreutils -maxdepth 1 -name '*.fern'`) {
		t.Fatalf("%s no longer derives its utility list from coreutils/*.fern; an enumerated "+
			"list goes stale silently, and a utility missing from it is never compiled by "+
			"the self-host leg in any shard. `find` specifically: a bare glob that matches "+
			"nothing expands to the literal pattern and reaches the -run alternation as a "+
			"stray `*` rather than tripping the lane's empty check", file)
	}
	if !strings.Contains(src, "scripts/shard-tests") {
		t.Fatalf("%s no longer partitions with scripts/shard-tests, which is what guarantees "+
			"every listed name lands in exactly one bucket", file)
	}
	if !strings.Contains(src, "re_escape") {
		t.Fatalf("%s joins derived names into a -run alternation without escaping them; "+
			"coreutils/[.fern alone makes that pattern an invalid regexp and its shard "+
			"red start to finish", file)
	}
	if !strings.Contains(src, `go test -list "$pat"`) {
		t.Fatalf("%s no longer compiles its -run patterns before using them; a metacharacter "+
			"name added to the tree later would surface as a shard-wide failure rather than "+
			"as the pattern that is wrong", file)
	}
	return lane{name: file + " (shards)", pkgs: []string{"internal/testing/coreutils"}, all: true}
}

func TestCoreutilsShardCountMatchesTheMatrix(t *testing.T) {
	src := workflowSource(t, "test-coreutils.yml")
	m := regexp.MustCompile(`(?m)^\s+NSHARD:\s*"(\d+)"`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("test-coreutils.yml sets no NSHARD; the shard jobs would partition into an empty count")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse NSHARD %q: %v", m[1], err)
	}
	sm := regexp.MustCompile(`(?m)^\s+shard:\s*\[([^\]]*)\]`).FindStringSubmatch(src)
	if sm == nil {
		t.Fatal("test-coreutils.yml has no `shard:` matrix axis")
	}
	var shards []int
	for _, f := range strings.Split(sm[1], ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parse shard index %q: %v", f, err)
		}
		shards = append(shards, v)
	}
	if len(shards) != n {
		t.Errorf("test-coreutils.yml partitions into NSHARD=%d buckets but its matrix runs %d shards %v — "+
			"every test the partition assigns to a bucket no job runs is selected by nothing and never runs",
			n, len(shards), shards)
	}
	sort.Ints(shards)
	for i, got := range shards {
		if got != i {
			t.Errorf("test-coreutils.yml's shard matrix is %v, which is not 0..%d — scripts/shard-tests "+
				"numbers buckets from zero and a gap is a bucket nothing runs", shards, n-1)
			break
		}
	}
}

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
	var kept []string
	for _, p := range prefixes {
		if !exact[p] {
			kept = append(kept, p)
		}
	}
	return lane{name: file, pkgs: []string{pkg}, exact: exact, prefixes: kept, patterns: patterns}
}

var testNameRe = regexp.MustCompile(`\bTest[A-Za-z0-9_]+[$|)]?`)

var selectorFlagRe = regexp.MustCompile(`-(?:test\.)?(?:run|list)[ =]+('[^']*'|"[^"]*"|[^\s]+)`)

func resolveSelector(src, arg string) *regexp.Regexp {
	arg = strings.Trim(arg, `'"`)
	for _, m := range envRefRe.FindAllStringSubmatch(arg, -1) {
		if v, ok := lookupEnv(src, m[1]); ok {
			arg = strings.ReplaceAll(arg, m[0], v)
		}
	}
	arg = strings.ReplaceAll(arg, `\$`, `$`)
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
	if re.MatchString("ZzNotATestNameAtAll") {
		return nil
	}
	return re
}

var envRefRe = regexp.MustCompile(`\$\{?([A-Z_][A-Z0-9_]*)\}?`)

func lookupEnv(src, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(?:"([^"]*)"|'([^']*)')\s*$`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		return "", false
	}
	return m[1] + m[2], true
}

func envValue(t *testing.T, src, key string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(?:"([^"]*)"|'([^']*)')\s*$`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no `%s:` entry found — this gate reads the lane's selection from it", key)
	}
	return m[1] + m[2]
}

func TestMacOSLaneFilterCoversWhatTheLaneBuilds(t *testing.T) {
	root := mustRepoRoot(t)
	entries := pathFilterEntries(t, "macos.yml")
	if len(entries) == 0 {
		t.Fatal("macos.yml has no `paths` filter in ci.yml's lane table — this gate assumes one")
	}

	for _, e := range entries {
		pat := strings.TrimPrefix(e, "!")
		if !pathPatternMatchesSomething(t, root, pat) {
			t.Errorf("macos.yml path filter entry %q matches no file in the tree — "+
				"it has been silently selecting nothing", e)
		}
	}

	closure := goListDeps(t, root, "./cmd/fern", "./internal/testing/e2e", "./internal/testing/e2ecompiler")
	for _, pkg := range closure {
		if !pathFilterSelects(entries, pkg+"/x.go") {
			t.Errorf("macos.yml does not run when %s changes, but the arm64-darwin "+
				"binary it builds and executes is compiled from it", pkg)
		}
	}
}

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

func globMatches(pattern, path string) bool {
	var b strings.Builder
	b.WriteString("^")
	atom := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
			atom = false
		case c == '*':
			b.WriteString("[^/]*")
			atom = false
		case (c == '?' || c == '+') && atom:
			b.WriteByte(c)
			atom = false
		case c == '[' && strings.IndexByte(pattern[i:], ']') > 0:
			j := i + strings.IndexByte(pattern[i:], ']')
			b.WriteString(pattern[i : j+1])
			i = j
			atom = true
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
			atom = true
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()).MatchString(path)
}

func TestGlobMatchesFollowsGitHubGrammar(t *testing.T) {
	for _, c := range []struct {
		pattern, path string
		want          bool
	}{
		{"src/x?.txt", "src/.txt", true},
		{"src/x?.txt", "src/x.txt", true},
		{"src/x?.txt", "src/xx.txt", false},
		{"a+b.txt", "aab.txt", true},
		{"a+b.txt", "b.txt", false},
		{"[ab]+.go", "abba.go", true},
		{"[ab]x.go", "cx.go", false},
		{"?x", "?x", true},
		{"+x", "+x", true},
		{"[a", "[a", true},
		{"*.md", "README.md", true},
		{"*.md", "docs/a.md", false},
		{"docs/**", "docs/a/b.md", true},
		{"a.b", "axb", false},
	} {
		if got := globMatches(c.pattern, c.path); got != c.want {
			t.Errorf("globMatches(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
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
			if rel == ".git" || rel == "build" || rel == "bin" || isNestedCheckout(root, p) {
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
