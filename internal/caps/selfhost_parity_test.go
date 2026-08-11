package caps

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The self-host compiler carries its own copy of this package
// (examples/self_host/caps.fern, #6634) because it cannot import Go. Two
// copies of a classification is exactly the shape CLAUDE.md warns about: a new
// builtin needs classifying in both capability systems, and the way that goes
// wrong is one side being updated and the other not — silently, because
// neither compiler can see the other's table.
//
// These tests read the Fern source as data and compare it entry-for-entry with
// the maps above, so a builtin tagged here and unclassified there fails a fast
// Go test rather than surfacing as "the self-host compiled a program native
// refuses". They deliberately do NOT build anything: the price of the gate has
// to be low enough that it runs on every change to this file.
//
// The behaviour on top of the table — reachability, the report, the E070
// text — is pinned by examples/self_host/caps_run.fern and by the
// native/self-host differential in internal/e2eselfhost.

const selfHostCapsSrc = "../../examples/self_host/caps.fern"

func readSelfHostCaps(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selfHostCapsSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostCapsSrc, err)
	}
	return string(b)
}

// fernStringList pulls the string literals out of a Fern array literal body.
func fernStringList(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// fernList extracts a `pub function NAME(): string[] { return [...]; }` body.
func fernList(t *testing.T, src, fn string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)` + fn + `\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("no %s() list found in %s — the extraction pattern has gone stale, which would make this test vacuous", fn, selfHostCapsSrc)
	}
	return fernStringList(m[1])
}

// TestSelfHostCapabilityVocabularyMatches pins the vocabulary. A capability
// one side knows and the other does not is a grant that parses in fern.toml
// and gates nothing.
func TestSelfHostCapabilityVocabularyMatches(t *testing.T) {
	got := fernList(t, readSelfHostCaps(t), "capabilities")
	if strings.Join(got, ",") != strings.Join(Capabilities, ",") {
		t.Errorf("vocabulary:\nnative    [%s]\nself-host [%s]", strings.Join(Capabilities, " "), strings.Join(got, " "))
	}
}

// TestSelfHostBuiltinCapsMatch pins the tagged half of the classification:
// every builtin tagged here is tagged there, with the same capability, and the
// self-host tags nothing extra.
func TestSelfHostBuiltinCapsMatch(t *testing.T) {
	src := readSelfHostCaps(t)
	re := regexp.MustCompile(`BuiltinCap\s*\{\s*builtin:\s*"([^"]+)",\s*capability:\s*"([^"]+)"\s*\}`)
	found := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		if prev, dup := found[m[1]]; dup {
			t.Errorf("the self-host tags %q twice (%q then %q)", m[1], prev, m[2])
		}
		found[m[1]] = m[2]
	}
	if len(found) == 0 {
		t.Fatalf("no BuiltinCap literals found in %s — the extraction pattern has gone stale, which would make this test vacuous", selfHostCapsSrc)
	}
	for builtin, capability := range BuiltinCaps {
		got, ok := found[builtin]
		if !ok {
			t.Errorf("builtin %q reaches '%s' here but is unclassified in the self-host — add a BuiltinCap for it in %s",
				builtin, capability, selfHostCapsSrc)
			continue
		}
		if got != capability {
			t.Errorf("builtin %q: '%s' here, '%s' in the self-host", builtin, capability, got)
		}
	}
	for builtin, capability := range found {
		if _, ok := BuiltinCaps[builtin]; !ok {
			t.Errorf("the self-host tags %q as '%s', which is untagged here", builtin, capability)
		}
	}
}

// TestSelfHostUngatedMatch pins the other half. A builtin that moves from
// ungated to tagged (or the reverse) on one side only is a package one
// compiler refuses and the other builds.
func TestSelfHostUngatedMatch(t *testing.T) {
	found := map[string]bool{}
	for _, name := range fernList(t, readSelfHostCaps(t), "ungated") {
		found[name] = true
	}
	for name := range Ungated {
		if !found[name] {
			t.Errorf("builtin %q is ungated here but absent from the self-host's ungated()", name)
		}
	}
	for name := range found {
		if !Ungated[name] {
			t.Errorf("the self-host ungates %q, which is not in Ungated here", name)
		}
	}
}

// TestSelfHostFrontendUngatedAreSelfHostOnly holds the one list with no native
// counterpart to its stated reason for existing: names the self-host front end
// registers as builtins that native's checker does not, so this package's
// completeness contract has nothing to say about them.
//
// Both directions matter. A name that native DOES know belongs in the shared
// ungated() list, where the test above compares it entry-for-entry — parking
// it here would exempt it from that comparison, which is the only way a
// classification difference could hide.
func TestSelfHostFrontendUngatedAreSelfHostOnly(t *testing.T) {
	src := readSelfHostCaps(t)
	extras := fernList(t, src, "frontend_ungated")
	if len(extras) == 0 {
		t.Fatalf("no frontend_ungated() list found in %s", selfHostCapsSrc)
	}
	shared := map[string]bool{}
	for _, name := range fernList(t, src, "ungated") {
		shared[name] = true
	}
	registered := selfHostBuiltinRegistry(t)
	for _, name := range extras {
		if _, tagged := BuiltinCaps[name]; tagged || Ungated[name] {
			t.Errorf("%q is classified in this package, so it belongs in the self-host's ungated() list, not frontend_ungated()", name)
		}
		if shared[name] {
			t.Errorf("%q is in both ungated() and frontend_ungated() in the self-host — the lists are a partition", name)
		}
		if !registered[name] {
			t.Errorf("%q is in frontend_ungated() but not in parser.builtin_function_names() — nothing can call it, so the entry is stale", name)
		}
	}
}

// TestSelfHostRegistryFullyClassified is the completeness contract on the
// self-host side, at Go speed: every builtin its parser registers is tagged,
// ungated, front-end-ungated, or `__`-internal. caps_run.fern asserts the same
// thing from inside the compiled compiler; this one fails in milliseconds when
// a builtin lands unclassified.
func TestSelfHostRegistryFullyClassified(t *testing.T) {
	src := readSelfHostCaps(t)
	classified := map[string]bool{}
	for _, name := range fernList(t, src, "ungated") {
		classified[name] = true
	}
	for _, name := range fernList(t, src, "frontend_ungated") {
		classified[name] = true
	}
	re := regexp.MustCompile(`BuiltinCap\s*\{\s*builtin:\s*"([^"]+)"`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		classified[m[1]] = true
	}
	var missing []string
	for name := range selfHostBuiltinRegistry(t) {
		if strings.HasPrefix(name, "__") || classified[name] {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("the self-host registers these builtins with no capability classification: %s\nclassify each in %s (BuiltinCap, ungated(), or frontend_ungated())",
			strings.Join(missing, " "), selfHostCapsSrc)
	}
}

// selfHostBuiltinRegistry reads parser.builtin_function_names() — the
// self-host's user-callable builtin registry, and the set the completeness
// contract is measured against.
func selfHostBuiltinRegistry(t *testing.T) map[string]bool {
	t.Helper()
	const parserSrc = "../../examples/self_host/parser.fern"
	b, err := os.ReadFile(parserSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", parserSrc, err)
	}
	m := regexp.MustCompile(`(?s)builtin_function_names\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no builtin_function_names() list found in %s", parserSrc)
	}
	// Strip comments first: the list is annotated, and a builtin NAME quoted
	// inside a comment would otherwise read as a registered builtin.
	body := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(m[1], "")
	out := map[string]bool{}
	for _, name := range fernStringList(body) {
		out[name] = true
	}
	if len(out) == 0 {
		t.Fatalf("builtin_function_names() extracted empty from %s", parserSrc)
	}
	return out
}
