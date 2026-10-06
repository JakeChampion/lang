package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The runtime-helper dependency graph in asmcore.fern is declared TWICE, and
// nothing made the two declarations agree.
//
//   - `mark_<X>()` marks the root and, by hand, whatever else that root's helper
//     body links against. It is what an emit site calls.
//   - `runtime_need_deps("<X>")` declares the same edges for `close_needs()`,
//     the transitive closure taken once before the runtime is emitted.
//
// Both exist because a need can arrive by either route: most emit sites call the
// wrapper, but some seed a root directly (`.need("x")` at an op site, the
// driver's `-ir-extra-need`), and those reach only the table. So an edge present
// in one and missing from the other is a helper that links or not depending on
// which route its need came in by.
//
// That had already drifted: `mark_str_trim` marked `str_concat` — a dep a
// trim-without-concat link failure proved, recorded in its own comment — while
// the table put `str_trim` in the heap-only bucket. Every str_trim need happens
// to arrive through the wrapper today, so it was latent rather than broken.
//
// Issue #2649's end state deletes both declarations in favour of the real call
// graph. Until then, this test is what keeps them from disagreeing.

var (
	// The body may open with comment lines before the return — mark_str_trim
	// does, and an earlier version of this pattern that required the return
	// immediately after the brace silently skipped the one wrapper this test
	// exists for. markDefRe below is what keeps that from recurring.
	markFnRe   = regexp.MustCompile(`pub function \(s: EmitState\) mark_([a-z0-9_]+)\(\): EmitState \{(?:\s*//[^\n]*\n)*\s*return s((?:\.need\("[a-z0-9_]+"\))+);`)
	markDefRe  = regexp.MustCompile(`pub function \(s: EmitState\) mark_([a-z0-9_]+)\(\): EmitState \{`)
	needCallRe = regexp.MustCompile(`\.need\("([a-z0-9_]+)"\)`)
	nameEqRe   = regexp.MustCompile(`name == "([a-z0-9_]+)"`)
)

func TestMarkWrappersAgreeWithRuntimeNeedDeps(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "compiler", "asmcore.fern"))
	if err != nil {
		t.Fatalf("read asmcore.fern: %v", err)
	}
	text := string(src)

	table := parseRuntimeNeedDeps(t, text)

	wrappers := markFnRe.FindAllStringSubmatch(text, -1)

	// Every mark_* DEFINITION must have been parsed. A count threshold is not
	// enough: the first version of this test matched 36 wrappers and still
	// skipped mark_str_trim, the single wrapper whose drift prompted it,
	// because that one opens with comment lines. A wrapper the pattern cannot
	// read is an unchecked edge, so it fails here rather than passing quietly.
	parsed := map[string]bool{}
	for _, w := range wrappers {
		parsed[w[1]] = true
	}
	for _, d := range markDefRe.FindAllStringSubmatch(text, -1) {
		if !parsed[d[1]] {
			t.Errorf("mark_%s is defined but its need chain did not parse — its edges are unchecked", d[1])
		}
	}
	if len(wrappers) < 20 {
		t.Fatalf("found only %d mark_* wrappers — the pattern no longer matches, so this test proves nothing", len(wrappers))
	}

	checked := 0
	for _, w := range wrappers {
		rootName, chain := w[1], w[2]
		var marked []string
		for _, m := range needCallRe.FindAllStringSubmatch(chain, -1) {
			marked = append(marked, m[1])
		}
		// A wrapper whose chain does not include its own root name is marking
		// on behalf of something else (mark_maps marks str_eq/arr_push, say);
		// the root-relative comparison below does not apply to it.
		if !contains(marked, rootName) {
			continue
		}
		checked++

		var wrapperDeps []string
		for _, n := range marked {
			if n != rootName {
				wrapperDeps = append(wrapperDeps, n)
			}
		}
		tableDeps := table[rootName]

		// The table may legitimately declare MORE than the wrapper: a root the
		// wrapper never marks directly can still be pulled in transitively.
		// What must not happen is the wrapper knowing an edge the table does
		// not, because then only the wrapper route carries it.
		for _, d := range wrapperDeps {
			if !contains(tableDeps, d) {
				t.Errorf("mark_%s marks %q but runtime_need_deps(%q) does not declare it: %v\n"+
					"a need seeded for %q WITHOUT going through mark_%s (an op site's own .need, or -ir-extra-need) "+
					"closes over the table alone and would not pull %q in",
					rootName, d, rootName, sorted(tableDeps), rootName, rootName, d)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d wrappers were root-marking — too few for this to be a real check", checked)
	}
	t.Logf("checked %d mark_* wrappers against runtime_need_deps", checked)
}

// parseRuntimeNeedDeps reads the declared edges out of runtime_need_deps. Each
// arm is `if (name == "x" || ...) { ... return v; }`, ending at the first `}`
// indented as far as its `if` line: every root its condition names gets the
// literal of the `let v: string[] = [...]` it returns, declared in the arm or
// elsewhere in the function. Every root the function tests must land in the
// table, so an arm the parse cannot read fails by name.
func parseRuntimeNeedDeps(t *testing.T, text string) map[string][]string {
	t.Helper()
	start := strings.Index(text, "pub function runtime_need_deps(name: string): string[] {")
	if start < 0 {
		t.Fatal("asmcore.fern has no runtime_need_deps — this test is vacuous")
	}
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}

	head := regexp.MustCompile(`(?m)^([ \t]*)if \(([^{}]*)\)\s*\{`)
	ret := regexp.MustCompile(`\breturn ([a-z0-9_]+);`)
	quoted := regexp.MustCompile(`"([a-z0-9_]+)"`)
	out := map[string][]string{}
	for _, m := range head.FindAllStringSubmatchIndex(body, -1) {
		roots := nameEqRe.FindAllStringSubmatch(body[m[4]:m[5]], -1)
		if len(roots) == 0 {
			continue
		}
		first := roots[0][1]
		rest := body[m[1]:]
		end := strings.Index(rest, "\n"+body[m[2]:m[3]]+"}")
		if end < 0 {
			t.Fatalf("runtime_need_deps arm %q has no closing brace at its own indent", first)
		}
		rets := ret.FindAllStringSubmatch(rest[:end], -1)
		if len(rets) == 0 {
			t.Fatalf("runtime_need_deps arm %q returns no named list", first)
		}
		v := rets[len(rets)-1][1]
		decl := regexp.MustCompile(`\blet ` + v + `: string\[\] = \[([^\]]*)\]`)
		l := decl.FindStringSubmatch(rest[:end])
		if l == nil {
			l = decl.FindStringSubmatch(body)
		}
		if l == nil {
			t.Fatalf("runtime_need_deps arm %q returns %s, which no string[] literal declares", first, v)
		}
		var deps []string
		for _, q := range quoted.FindAllStringSubmatch(l[1], -1) {
			deps = append(deps, q[1])
		}
		for _, r := range roots {
			out[r[1]] = deps
		}
	}
	for _, m := range nameEqRe.FindAllStringSubmatch(body, -1) {
		if _, ok := out[m[1]]; !ok {
			t.Errorf("runtime_need_deps tests %q, but no arm the parse read names it", m[1])
		}
	}
	if len(out) < 10 {
		t.Fatalf("parsed only %d runtime_need_deps roots — the parse no longer matches the source", len(out))
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func sorted(xs []string) []string {
	c := append([]string(nil), xs...)
	sort.Strings(c)
	return c
}
