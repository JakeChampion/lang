package selfhostsrc

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// `ircore.all_runtime_need_roots()` calls itself "the closed set of runtime-need
// ROOT names — every string the codegen ever passes to `s.need(…)`", and the
// per-module driver feeds exactly that list to the ENTRY module's shared runtime
// instead of the exact per-module union. The soundness argument is that the list
// is an over-approximation: a root missing from it is a module linking against an
// undefined `__fern_*`.
//
// The list was maintained by hand, and the gaps do not announce themselves. A
// root is only reachable from a LIBRARY-only module — nothing in the compiler's
// own per-module build calls `w.flags()`, `w.write_some()` or `__heap_alloc_count()`
// — so the per-module link fixtures the comment names never ask the entry unit for
// those bodies, and the gate reads green with the row absent. Four roots
// (`fd_flags`, `write_some`, `open_with`, `crc32_cksum`) sat missing that way under
// #9353, and `alloc_count` went missing the same way the day #9596 landed.
//
// So this derives the set mechanically and pins the invariant instead. It builds
// nothing and reads the Fern sources as data, which is what makes it cheap enough
// to run on every change to them.
var needRootSources = []string{
	"../../examples/self_host/asm_ir.fern",
	"../../examples/self_host/asm_arm64_ir.fern",
	"../../examples/self_host/irlower.fern",
	"../../examples/self_host/ircore.fern",
	"../../examples/self_host/asmcore.fern",
}

// marksNeed matches a root marked with a STRING LITERAL: `s.need("x")`. A root
// marked through a variable (`s.need(sig_fn)` — signal_ignore / signal_default)
// is invisible here by construction, which is why the list stays hand-written
// and this test only proves one direction: everything spelled as a literal is
// listed. Extra rows are fine and expected; the over-approximation is the point.
var marksNeed = regexp.MustCompile(`\.need\("([a-z0-9_]+)"\)`)

// allRuntimeNeedRootsBody isolates the `return [ … ];` of
// ircore.all_runtime_need_roots so a `need("…")` elsewhere in ircore.fern cannot
// be mistaken for a listed row.
func allRuntimeNeedRootsBody(t *testing.T) string {
	t.Helper()
	const path = "../../examples/self_host/ircore.fern"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	const decl = "pub function all_runtime_need_roots(): string[] {"
	i := strings.Index(string(src), decl)
	if i < 0 {
		t.Fatalf("%s: all_runtime_need_roots not found — did it move or get renamed?", path)
	}
	body, _, ok := strings.Cut(string(src)[i:], "\n}")
	if !ok {
		t.Fatalf("%s: all_runtime_need_roots has no closing brace", path)
	}
	return body
}

func TestAllRuntimeNeedRootsCoversEveryLiteralNeed(t *testing.T) {
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([a-z0-9_]+)"`).FindAllStringSubmatch(allRuntimeNeedRootsBody(t), -1) {
		listed[m[1]] = true
	}
	if len(listed) == 0 {
		t.Fatal("all_runtime_need_roots parsed as empty — the extraction is broken, not the list")
	}

	// `x` is the doc comment's own example ("If a NEW .need(\"x\") root is
	// added…"), not a root. It is the only literal in these files that is
	// documentation rather than code.
	ignored := map[string]bool{"x": true}

	marked := map[string][]string{}
	for _, path := range needRootSources {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range marksNeed.FindAllStringSubmatch(string(src), -1) {
			if !ignored[m[1]] {
				marked[m[1]] = append(marked[m[1]], path)
			}
		}
	}
	if len(marked) == 0 {
		t.Fatal("no .need(\"…\") sites found — the extraction is broken, not the sources")
	}

	var missing []string
	for name := range marked {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		sites := marked[name]
		sort.Strings(sites)
		t.Errorf("runtime need root %q is marked by %s but is absent from ircore.all_runtime_need_roots():\n"+
			"    a per-module build of a module whose only reach is this root links against an undefined __fern_%s.\n"+
			"    Add the row — the list is an over-approximation, so adding one is sound by construction.",
			name, strings.Join(sites, ", "), name)
	}
}
