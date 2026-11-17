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

// A has_need gate also counts: it catches a need that is marked through a
// computed name, such as ir.sha_blocks_need.
var marksNeed = regexp.MustCompile(`\.(?:has_)?need\("([a-z0-9_]+)"\)`)

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

// byteCopyKernels is the list that rtbytecopy.kernel_names returns.
// asmcore.need_array_helper marks each name as a need through a variable,
// which marksNeed cannot see.
func byteCopyKernels(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("rtbytecopy.fern")
	if err != nil {
		t.Fatalf("read rtbytecopy.fern: %v", err)
	}
	const decl = "pub function kernel_names(): string[] {"
	i := strings.Index(string(src), decl)
	if i < 0 {
		t.Fatal("rtbytecopy.fern: kernel_names not found — did it move or get renamed?")
	}
	body, _, _ := strings.Cut(string(src)[i:], "\n}")
	var out []string
	for _, m := range regexp.MustCompile(`"([a-z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		t.Fatal("kernel_names parsed as empty — the extraction is broken, not the list")
	}
	return out
}

func TestAllRuntimeNeedRootsCoversEveryLiteralNeed(t *testing.T) {
	listed := map[string]bool{}
	body := allRuntimeNeedRootsBody(t)
	for _, m := range regexp.MustCompile(`"([a-z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		if listed[m[1]] {
			t.Errorf("runtime need root %q is in ircore.all_runtime_need_roots() more than one time: remove the second row", m[1])
		}
		listed[m[1]] = true
	}
	kernels := byteCopyKernels(t)
	if strings.Contains(body, "rtbytecopy.kernel_names()") {
		for _, name := range kernels {
			listed[name] = true
		}
	}
	if len(listed) == 0 {
		t.Fatal("all_runtime_need_roots parsed as empty — the extraction is broken, not the list")
	}
	// asmcore marks each byte-copy kernel by its name, which no literal shows.
	if !strings.Contains(allRuntimeNeedRootsBody(t), "rtbytecopy.kernel_names()") {
		t.Error("all_runtime_need_roots does not list rtbytecopy.kernel_names(), so a per-module build that calls a kernel has no definition of it")
	}

	ignored := map[string]bool{
		"x": true,
		// runtime_need_deps adds it to each listed root that reads text.
		"utf8_valid": true,
		// The -cover counter slot. A per-module build is never instrumented.
		"raw_cover": true,
	}

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
	for _, name := range kernels {
		marked[name] = append(marked[name], "asmcore.fern")
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
