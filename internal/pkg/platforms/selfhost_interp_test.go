package platforms

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestSelfHostInterpImplementsHostedBuiltins(t *testing.T) {
	const path = "../../../compiler/interp.fern"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(b)
	dispatched := map[string]bool{}
	for _, m := range regexp.MustCompile(`name == "([^"]+)"`).FindAllStringSubmatch(src, -1) {
		dispatched[m[1]] = true
	}
	for _, body := range regexp.MustCompile(`(?s)match \(name\) \{(.*?)\n\s*\};`).FindAllStringSubmatch(src, -1) {
		for _, arm := range regexp.MustCompile(`(?m)^\s*("[^"]+"(?:\s*\|\s*"[^"]+")*)\s*=>`).FindAllStringSubmatch(body[1], -1) {
			for _, n := range fernStringList(arm[1]) {
				dispatched[n] = true
			}
		}
	}
	for _, m := range regexp.MustCompile(`has_str\(\s*\[([^\]]*)\],\s*name,?\s*\)`).FindAllStringSubmatch(src, -1) {
		for _, n := range fernStringList(m[1]) {
			dispatched[n] = true
		}
	}
	for _, m := range regexp.MustCompile(`has_str\((\w+),\s*name\)`).FindAllStringSubmatch(src, -1) {
		set := regexp.MustCompile(`(?m)^const ` + m[1] + `: string\[\] = \[([^\]]*)\];`).FindStringSubmatch(src)
		if set == nil {
			t.Fatalf("%s tests name against %s, which is not a string list constant of the module", path, m[1])
		}
		for _, n := range fernStringList(set[1]) {
			dispatched[n] = true
		}
	}
	// A shared name predicate of checker.fern dispatches the names it tests.
	for _, m := range regexp.MustCompile(`checker\.(\w+)\(name\)`).FindAllStringSubmatch(src, -1) {
		for _, n := range predicateNames(t, "../../../compiler/checker.fern", m[1]) {
			dispatched[n] = true
		}
	}
	if !dispatched["print"] || !dispatched["read_file"] {
		t.Fatalf("the dispatch extracted from %s lacks print or read_file — the patterns have gone stale, which would make this test vacuous", path)
	}
	granted := map[string]bool{}
	for _, profile := range []string{"hosted-linux", "hosted-darwin"} {
		for _, c := range capabilityProfiles[profile] {
			granted[c] = true
		}
	}
	var missing []string
	for name, capability := range gatedBuiltins {
		if granted[capability] && !dispatched[name] {
			missing = append(missing, name)
		}
	}
	for name := range coreBuiltins {
		if !dispatched[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d builtin(s) a hosted target provides that %s does not dispatch: %s\n"+
			"Each one stops an interpreted program with `undefined function`. Answer it in the\n"+
			"interpreter's host builtins, matching internal/oracle/interp.",
			len(missing), path, strings.Join(missing, ", "))
	}
}

// predicateNames lists the names that the one-line predicate fn of the Fern
// source at path compares its name parameter with.
func predicateNames(t *testing.T, path, fn string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	m := regexp.MustCompile(`function ` + fn + `\(name: string\): boolean \{\s*return ([^;]*);`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s has no one-line predicate %s(name: string): boolean", path, fn)
	}
	var out []string
	for _, q := range regexp.MustCompile(`name == "([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}
