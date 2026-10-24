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
	for _, m := range regexp.MustCompile(`has_str\(\[([^\]]*)\], name\)`).FindAllStringSubmatch(src, -1) {
		for _, n := range fernStringList(m[1]) {
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
