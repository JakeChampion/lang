package platforms

import (
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

const selfHostPlatformsSrc = "../../../compiler/platforms.fern"

func readSelfHostPlatforms(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selfHostPlatformsSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostPlatformsSrc, err)
	}
	return string(b)
}

func fernStringList(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestSelfHostGatedBuiltinsMatch(t *testing.T) {
	src := readSelfHostPlatforms(t)
	gateRe := regexp.MustCompile(`Gate\s*\{\s*builtin:\s*"([^"]+)",\s*capability:\s*"([^"]+)"\s*\}`)
	found := map[string]string{}
	for _, m := range gateRe.FindAllStringSubmatch(src, -1) {
		if prev, dup := found[m[1]]; dup {
			t.Errorf("self-host gates %q twice (%q then %q)", m[1], prev, m[2])
		}
		found[m[1]] = m[2]
	}
	if len(found) == 0 {
		t.Fatalf("no Gate literals found in %s — the extraction pattern has gone stale, which would make this test vacuous", selfHostPlatformsSrc)
	}
	for builtin, capability := range gatedBuiltins {
		got, ok := found[builtin]
		if !ok {
			t.Errorf("builtin %q is gated on `%s` here but unclassified in the self-host — add a Gate for it in %s",
				builtin, capability, selfHostPlatformsSrc)
			continue
		}
		if got != capability {
			t.Errorf("builtin %q: gated on `%s` here, `%s` in the self-host", builtin, capability, got)
		}
	}
	for builtin, capability := range found {
		if _, ok := gatedBuiltins[builtin]; !ok {
			t.Errorf("the self-host gates %q on `%s`, which is ungated here", builtin, capability)
		}
	}
}

func TestSelfHostCoreBuiltinsMatch(t *testing.T) {
	src := readSelfHostPlatforms(t)
	body := regexp.MustCompile(`(?s)core_builtins\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(src)
	if body == nil {
		t.Fatalf("no core_builtins() list found in %s", selfHostPlatformsSrc)
	}
	found := map[string]bool{}
	for _, name := range fernStringList(body[1]) {
		found[name] = true
	}
	for name := range coreBuiltins {
		if !found[name] {
			t.Errorf("builtin %q is core here but not in the self-host's core_builtins()", name)
		}
	}
	for name := range found {
		if !coreBuiltins[name] {
			t.Errorf("the self-host calls %q core, which is not core here", name)
		}
	}
}

func TestSelfHostCapabilityProfilesMatch(t *testing.T) {
	src := readSelfHostPlatforms(t)
	profRe := regexp.MustCompile(`(?s)if \(profile == "([a-z-]+)"\) \{.*?return \[(.*?)\];`)
	found := map[string][]string{}
	for _, m := range profRe.FindAllStringSubmatch(src, -1) {
		found[m[1]] = fernStringList(m[2])
	}
	if len(found) == 0 {
		t.Fatalf("no capability profiles found in %s", selfHostPlatformsSrc)
	}
	for name, want := range capabilityProfiles {
		got, ok := found[name]
		if !ok {
			if len(want) == 0 {
				continue
			}
			t.Errorf("profile %q is missing from the self-host's capability_profile()", name)
			continue
		}
		if !slices.Equal(want, got) {
			t.Errorf("profile %q: native grants [%s], the self-host grants [%s]",
				name, strings.Join(want, " "), strings.Join(got, " "))
		}
	}
	for name := range found {
		if _, ok := capabilityProfiles[name]; !ok {
			t.Errorf("the self-host declares profile %q, which does not exist here", name)
		}
	}
}

func TestSelfHostTargetNamesMatch(t *testing.T) {
	got := fernStringList(regexp.MustCompile(`(?s)pub function targets\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(readSelfHostPlatforms(t))[1])
	if len(got) == 0 {
		t.Fatalf("no targets() list found in %s — the extraction pattern has gone stale", selfHostPlatformsSrc)
	}
	want := Targets()
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	if strings.Join(sorted, " ") != strings.Join(got, " ") {
		t.Errorf("the self-host's targets() is not sorted: %v — `fern -targets` prints it in this order", got)
	}
	for _, name := range want {
		if slices.Contains(got, name) {
			continue
		}
		t.Errorf("target %q exists here but not in the self-host's targets()", name)
	}
	for _, name := range got {
		if !slices.Contains(want, name) {
			t.Errorf("the self-host declares target %q, which does not exist here", name)
		}
	}
}

func TestSelfHostListingMatches(t *testing.T) {
	src := readSelfHostPlatforms(t)
	descBody := regexp.MustCompile(`(?s)pub function description\(name: string\): string \{(.*?)\n\}`).FindStringSubmatch(src)
	if descBody == nil {
		t.Fatalf("no description() found in %s — the extraction pattern has gone stale", selfHostPlatformsSrc)
	}
	descs := map[string]string{}
	for _, m := range regexp.MustCompile(`if \(name == "([^"]+)"\) \{\s*return "([^"]*)";`).FindAllStringSubmatch(descBody[1], -1) {
		descs[m[1]] = m[2]
	}
	kindsBody := regexp.MustCompile(`(?s)pub function handler_kinds\(name: string\): string\[\] \{(.*?)\n\}`).FindStringSubmatch(src)
	if kindsBody == nil {
		t.Fatalf("no handler_kinds() found in %s — the extraction pattern has gone stale", selfHostPlatformsSrc)
	}
	kinds := map[string][]string{}
	for _, m := range regexp.MustCompile(`(?s)if \(([^{]*)\) \{\s*return \[(.*?)\];`).FindAllStringSubmatch(kindsBody[1], -1) {
		for _, env := range fernStringList(m[1]) {
			kinds[env] = fernStringList(m[2])
		}
	}
	if len(descs) == 0 || len(kinds) == 0 {
		t.Fatalf("extracted %d descriptions and %d handler-kind lists from %s — the extraction pattern has gone stale", len(descs), len(kinds), selfHostPlatformsSrc)
	}
	for _, name := range Targets() {
		d := ForTarget(name)
		if descs[name] != d.Description {
			t.Errorf("target %q: description here %q, in the self-host %q", name, d.Description, descs[name])
		}
		if !slices.Equal(d.HandlerKinds, kinds[d.Environment]) {
			t.Errorf("target %q: handler kinds here %v, in the self-host %v", name, d.HandlerKinds, kinds[d.Environment])
		}
	}
}

func TestSelfHostEquivalentsMatch(t *testing.T) {
	body := regexp.MustCompile(`(?s)function equivalent\(builtin: string\): string \{(.*?)\n\}`).FindStringSubmatch(readSelfHostPlatforms(t))
	if body == nil {
		t.Fatalf("no equivalent() found in %s — the extraction pattern has gone stale", selfHostPlatformsSrc)
	}
	found := map[string]string{}
	for _, m := range regexp.MustCompile(`if \(builtin == "([^"]+)"\) \{\s*return "([^"]+)";`).FindAllStringSubmatch(body[1], -1) {
		found[m[1]] = m[2]
	}
	if !maps.Equal(found, equivalents) {
		t.Errorf("equivalents here %v, in the self-host %v", equivalents, found)
	}
}
