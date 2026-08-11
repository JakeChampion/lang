package caps

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The self-host compiler carries its own copy of this inventory
// (examples/self_host/caps.fern, #6634) because it cannot import Go.
//
// This package already fails when a builtin lands unclassified HERE — the
// registry-completeness tests enumerate the checker's and the interpreter's
// builtins and require each to be in exactly one of BuiltinCaps or Ungated.
// Those tests cannot see the Fern table, so without the tests below a builtin
// would be classified in one compiler and invisible in the other, which is the
// failure CLAUDE.md describes as compounding silently: the self-host side is
// unclassified BY CONSTRUCTION and nothing complains.
//
// Reading the Fern source as data keeps the gate cheap enough to run on every
// change to either side — these build nothing.

const selfHostCapsSrc = "../../examples/self_host/caps.fern"

func readSelfHostCaps(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selfHostCapsSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostCapsSrc, err)
	}
	return string(b)
}

func fernStrings(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestSelfHostBuiltinCapsMatch pins the tagged half: every builtin tagged here
// is tagged there with the same capability, and the self-host tags nothing
// extra. A disagreement is a package that one compiler would refuse a grant
// for and the other would wave through.
func TestSelfHostBuiltinCapsMatch(t *testing.T) {
	src := readSelfHostCaps(t)
	capRe := regexp.MustCompile(`Cap\s*\{\s*builtin:\s*"([^"]+)",\s*capability:\s*"([^"]+)"\s*\}`)
	found := map[string]string{}
	for _, m := range capRe.FindAllStringSubmatch(src, -1) {
		if prev, dup := found[m[1]]; dup {
			t.Errorf("self-host tags %q twice (%q then %q)", m[1], prev, m[2])
		}
		found[m[1]] = m[2]
	}
	if len(found) == 0 {
		t.Fatalf("no Cap literals found in %s — the extraction pattern has gone stale, which would make this test vacuous", selfHostCapsSrc)
	}
	for builtin, capability := range BuiltinCaps {
		got, ok := found[builtin]
		if !ok {
			t.Errorf("builtin %q needs '%s' here but is unclassified in the self-host — add a Cap for it in %s",
				builtin, capability, selfHostCapsSrc)
			continue
		}
		if got != capability {
			t.Errorf("builtin %q: '%s' here, '%s' in the self-host", builtin, capability, got)
		}
	}
	for builtin, capability := range found {
		if _, ok := BuiltinCaps[builtin]; !ok {
			t.Errorf("the self-host tags %q as '%s', which needs no capability here", builtin, capability)
		}
	}
}

// TestSelfHostUngatedMatch pins the other half. A builtin moving between the
// halves on one side only changes what a grant means, not just what a report
// prints.
func TestSelfHostUngatedMatch(t *testing.T) {
	src := readSelfHostCaps(t)
	body := regexp.MustCompile(`(?s)ungated\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(src)
	if body == nil {
		t.Fatalf("no ungated() list found in %s", selfHostCapsSrc)
	}
	found := map[string]bool{}
	for _, name := range fernStrings(body[1]) {
		found[name] = true
	}
	for name := range Ungated {
		if !found[name] {
			t.Errorf("builtin %q is ungated here but missing from the self-host's ungated()", name)
		}
	}
	for name := range found {
		if !Ungated[name] {
			t.Errorf("the self-host calls %q ungated, which is not ungated here", name)
		}
	}
}

// TestSelfHostCapabilityVocabularyMatch pins the vocabulary itself. A word
// present on one side only is a grant a manifest can write and one compiler
// cannot honour.
func TestSelfHostCapabilityVocabularyMatch(t *testing.T) {
	src := readSelfHostCaps(t)
	body := regexp.MustCompile(`(?s)capabilities\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(src)
	if body == nil {
		t.Fatalf("no capabilities() list found in %s", selfHostCapsSrc)
	}
	got := fernStrings(body[1])
	want := append([]string(nil), Capabilities...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("capability vocabulary differs: native [%s], self-host [%s]",
			strings.Join(want, " "), strings.Join(got, " "))
	}
}
