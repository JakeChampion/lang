package e2eselfhost

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Drift gate for the import-free self-host modules.
//
// Ten of the 94 modules in examples/self_host carry no `import` statement at
// all. That is deliberate and load-bearing rather than an accident: several
// e2eselfhost drivers build a single-module program by CONCATENATING one of
// these files with a `main()`, which only works while the file pulls in
// nothing. x86_native.fern says so at its head —
//
//	This module is intentionally **import-free** so the native emitter can
//	`import "./x86_encode"` and the unit test can concatenate it with a
//	driver `main()` for the single-module wasm_run driver.
//
// — and self_host_arm64_linux_elf_test.go, self_host_arm64_encode_test.go and
// self_host_arm64_gas_test.go are three of the drivers that rely on it.
//
// The cost is duplication: parse_f64_bits (139 lines) plus pf64_mul2 / pf64_div2
// / pf64_sub / pf64_all_zero exist in four copies each, ~410 redundant lines.
// Sharing them through util.fern would break the concatenation drivers, so the
// copies are the correct design and this test does NOT ask anyone to remove
// them.
//
// What it asks is that they stay in step. A bug fixed in one copy of 139 lines
// of IEEE-754 bit manipulation and not the other three is silent: every driver
// still builds, every fixpoint still converges, and float parsing quietly
// depends on which module handled it. Nothing else in the tree would notice.
//
// Comparison is on CODE, not text: `//` comments are stripped and a leading
// `pub ` is normalised away, because those legitimately differ between copies
// today (util.fern's parse_f64_bits is `pub` and carries fewer explanatory
// comments than the three backend copies, while the executable lines are
// identical). Diverging on a comment is not a bug; diverging on a statement is.
func TestImportFreeModulesDoNotDrift(t *testing.T) {
	paths, err := filepath.Glob(langSrcAbs(t, filepath.Join("examples", "self_host", "*.fern")))
	if err != nil {
		t.Fatalf("glob self-host sources: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no self-host sources found")
	}

	type copyOf struct {
		module string
		hash   string
		lines  int
	}
	bodies := map[string][]copyOf{}
	var free []string

	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		lines := strings.Split(string(src), "\n")
		if hasImport(lines) {
			continue
		}
		mod := strings.TrimSuffix(filepath.Base(p), ".fern")
		free = append(free, mod)
		for name, body := range topLevelFuncs(lines) {
			code := normaliseCode(body)
			sum := md5.Sum([]byte(strings.Join(code, "\n")))
			bodies[name] = append(bodies[name], copyOf{mod, hex.EncodeToString(sum[:]), len(code)})
		}
	}

	// The import-free set is itself worth pinning. If a module gains an import
	// the concatenation drivers that depend on it break, and the failure lands
	// far from the cause — so say it here, where the reason is written down.
	sort.Strings(free)
	wantFree := []string{
		"arm64_native", "builtins", "elf", "lexer", "literate",
		"util", "watbin", "wit_compose", "wit_decode", "x86_native",
	}
	if strings.Join(free, ",") != strings.Join(wantFree, ",") {
		t.Errorf("the import-free module set changed.\n  got:  %v\n  want: %v\n"+
			"    These modules carry no `import` so e2eselfhost drivers can concatenate them with a\n"+
			"    driver main() — see x86_native.fern's header. A module that GAINED an import breaks\n"+
			"    those drivers; one that LOST its last import can join this list. Update wantFree and\n"+
			"    say in the commit message which driver you checked.", free, wantFree)
	}

	// Names that legitimately differ. These are small local helpers that happen
	// to share a name across modules with genuinely different behaviour, not
	// copies that drifted — verified by reading all four pairs.
	allowed := map[string]string{
		"contains":     "literate's takes a string[] haystack; util's takes a string and searches for a substring.",
		"index_of_str": "watbin's scans its own name table and returns a section index; util's is the generic array search.",
		"main":         "every driver entry point is different by definition.",
		"trim":         "literate's trims only spaces (chunk headers are space-delimited); util's also trims tabs and CR.",
	}

	var names []string
	for n := range bodies {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		copies := bodies[name]
		if len(copies) < 2 {
			continue
		}
		distinct := map[string]bool{}
		for _, c := range copies {
			distinct[c.hash] = true
		}
		if len(distinct) == 1 {
			continue
		}
		if why, ok := allowed[name]; ok {
			t.Logf("%s: %d copies differ, allowed — %s", name, len(copies), why)
			continue
		}
		var detail []string
		for _, c := range copies {
			detail = append(detail, fmt.Sprintf("        %-18s %s  %d code lines", c.module, c.hash[:12], c.lines))
		}
		sort.Strings(detail)
		t.Errorf("%s has %d copies across import-free modules and they have DIVERGED (%d distinct bodies):\n%s\n"+
			"    These modules cannot import each other — e2eselfhost drivers concatenate them with a\n"+
			"    driver main(), which only works while they pull in nothing — so the duplication is\n"+
			"    deliberate and the copies must be kept in step BY HAND. A fix applied to one and not\n"+
			"    the others is silent: every driver still builds and the fixpoint still converges.\n"+
			"    Apply the change to every copy. If the copies are genuinely meant to differ, add the\n"+
			"    name to `allowed` above with the reason.",
			name, len(copies), len(distinct), strings.Join(detail, "\n"))
	}
}

var (
	importRe = regexp.MustCompile(`^import\s`)
	funcRe   = regexp.MustCompile(`^(?:pub )?function (\w+)`)
	lineCmt  = regexp.MustCompile(`\s*//.*$`)
)

func hasImport(lines []string) bool {
	for _, l := range lines {
		if importRe.MatchString(l) {
			return true
		}
	}
	return false
}

// topLevelFuncs returns each top-level `function` body, keyed by name. A body
// runs from its declaration to the first column-0 `}` — the shape every
// top-level declaration in these modules has.
func topLevelFuncs(lines []string) map[string][]string {
	out := map[string][]string{}
	for i := 0; i < len(lines); i++ {
		m := funcRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		j := i
		for j < len(lines) && lines[j] != "}" {
			j++
		}
		if j < len(lines) {
			out[m[1]] = append([]string{}, lines[i:j+1]...)
		}
		i = j
	}
	return out
}

// normaliseCode strips comments, drops blank lines and removes a leading `pub `
// from the declaration, leaving only the statements. Two copies that differ
// solely in commentary or visibility compare equal.
func normaliseCode(body []string) []string {
	var out []string
	for i, l := range body {
		if i == 0 {
			l = strings.Replace(l, "pub ", "", 1)
		}
		l = strings.TrimRight(lineCmt.ReplaceAllString(l, ""), " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}
