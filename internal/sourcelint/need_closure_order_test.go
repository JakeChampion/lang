package sourcelint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A driver route seeds runtime needs, takes the declarative closure
// (`close_needs`), and then emits each helper gated on `has_need`. The order
// matters because close_needs is what turns a root into its declared deps:
// marking `str_split` is what puts `heap` in the set. A gate that reads the set
// before the closure therefore sees a SUBSET of what actually gets emitted.
//
// arm64 had that backwards. `_start` gates its envp save on `heap`
// (emit_ir_start — without the save, env()'s walk dereferences a null base),
// and both arm64 routes emitted `_start` and closed afterwards. It was latent
// only because every op site that seeds a heap-implying root happens to mark
// `heap` alongside it, so `heap` never arrived by the closure alone. Nothing
// checked that, and the failure it guards is a null deref at runtime rather
// than a link error.
//
// This pins the order at the source level. An emitted-asm test cannot: the fix
// is byte-neutral against every program that compiles today, which is the same
// property that made the bug invisible.
func TestCloseNeedsPrecedesEveryRuntimeGate(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	volatile := volatileNeeds(t, root)
	// A read of a need close_needs can ADD is the one that changes answer with
	// the ordering. Marker needs (`strfldok:T`, `field_reclaim:T`) have no
	// declared deps, so the per-function emit loop may read those freely — and
	// must, since it runs before the closure to seed the roots in the first
	// place.
	volRe := regexp.MustCompile(`has_need\("(` + strings.Join(volatile, "|") + `)"\)`)

	// The runtime-emission entry points: everything that decides WHICH __fern_*
	// bodies (or which half of `_start`) reaches the output. Extend this when a
	// new one appears — a consumer missing from here is unchecked.
	consumers := []string{
		"emit_ir_start(", "emit_ir_runtime(", "emit_runtime(", "emit_body(",
		"emit_rt_heap(", "emit_rt_io_and_string(", "emit_rt_collections_and_proc(",
	}

	routes := 0
	for _, name := range []string{"asm_ir.fern", "asm_arm64_ir.fern"} {
		src, err := os.ReadFile(filepath.Join(root, "examples", "self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, fn := range topLevelFuncs(string(src)) {
			body := stripLineComments(fn.body)
			close := strings.Index(body, "close_needs()")
			if close < 0 {
				continue
			}
			routes++
			before := body[:close]
			if m := volRe.FindStringSubmatch(before); m != nil {
				t.Errorf("%s: %s reads has_need(%q) BEFORE its close_needs() — %q is a need close_needs can add, "+
					"so that gate sees a subset of what the runtime emits", name, fn.name, m[1], m[1])
			}
			for _, c := range consumers {
				if strings.HasPrefix(c, fn.name+"(") {
					continue // its own definition / a self-recursive call
				}
				if strings.Contains(before, c) {
					t.Errorf("%s: %s calls %s BEFORE its close_needs() — that consumer gates on a need "+
						"the closure has not expanded yet", name, fn.name, strings.TrimSuffix(c, "("))
				}
			}
		}
	}
	if routes < 4 {
		t.Fatalf("found only %d close_needs routes — the scan no longer matches the source, so this proves nothing", routes)
	}
	t.Logf("checked %d close_needs routes against %d volatile needs", routes, len(volatile))
}

// volatileNeeds is every name runtime_need_deps can ADD to the set: the union of
// the dep lists it returns. Derived from the table rather than hard-coded, so a
// new edge widens this check instead of silently escaping it.
func volatileNeeds(t *testing.T, root string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, "examples", "self_host", "asmcore.fern"))
	if err != nil {
		t.Fatalf("read asmcore.fern: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "pub function runtime_need_deps(name: string): string[] {")
	if start < 0 {
		t.Fatal("asmcore.fern has no runtime_need_deps — this test is vacuous")
	}
	body = body[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	set := map[string]bool{"heap": true} // the shared `heap` bucket's own slice
	for _, lit := range regexp.MustCompile(`string\[\] = \[([^\]]*)\]`).FindAllStringSubmatch(body, -1) {
		for _, q := range regexp.MustCompile(`"([a-z0-9_]+)"`).FindAllStringSubmatch(lit[1], -1) {
			set[q[1]] = true
		}
	}
	if len(set) < 4 {
		t.Fatalf("parsed only %d volatile needs out of runtime_need_deps — the parse no longer matches", len(set))
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type fernFunc struct{ name, body string }

// topLevelFuncs splits a .fern source at each column-0 `function` /
// `pub function`, so a body runs to the next definition.
func topLevelFuncs(src string) []fernFunc {
	head := regexp.MustCompile(`(?m)^(?:pub )?function (?:\([^)]*\) )?([a-z_0-9]+)`)
	locs := head.FindAllStringSubmatchIndex(src, -1)
	var out []fernFunc
	for i, m := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out = append(out, fernFunc{name: src[m[2]:m[3]], body: src[m[0]:end]})
	}
	return out
}

// stripLineComments blanks `//` comments so a consumer NAMED in prose ("see the
// emit_ir_runtime(s, false) below") is not read as a call. Length is preserved
// only incidentally; callers use this text on its own.
func stripLineComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
