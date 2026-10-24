package compiler

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var needRootSources = []string{
	"asm_ir.fern",
	"asm_arm64_ir.fern",
	"irtables.fern",
	"lift.fern",
	"ircore.fern",
	"asmcore.fern",
}

var marksNeed = regexp.MustCompile(`\.need\("([a-z0-9_]+)"\)`)

func allRuntimeNeedRootsBody(t *testing.T) string {
	t.Helper()
	const path = "ircore.fern"
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
