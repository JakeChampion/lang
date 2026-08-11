package platforms

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The self-host compiler carries its own copy of this package
// (examples/self_host/platforms.fern, #6633) because it cannot import Go. Two
// copies of a classification is exactly the shape CLAUDE.md warns about: a new
// builtin needs classifying in both capability systems, and the way that goes
// wrong is one side being updated and the other not — silently, because
// neither compiler can see the other's table.
//
// These tests read the Fern source as data and compare it entry-for-entry with
// the maps above, so a builtin gated here and unclassified there fails a fast
// Go test rather than surfacing as "the self-host compiled a program native
// refuses". They deliberately do NOT build anything: the price of the gate has
// to be low enough that it runs on every change to this file.
//
// What they do not cover: the target NAMES, which still differ (#6635 —
// the self-host driver's targets are bare ISA names). Capability sets are
// compared per PROFILE, which is the half that carries the meaning.

const selfHostPlatformsSrc = "../../examples/self_host/platforms.fern"

func readSelfHostPlatforms(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(selfHostPlatformsSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostPlatformsSrc, err)
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

// TestSelfHostGatedBuiltinsMatch pins the gated half of the classification:
// every builtin gated here is gated there, by the same capability, and the
// self-host gates nothing extra.
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

// TestSelfHostCoreBuiltinsMatch pins the other half. A builtin that moves from
// core to gated (or the reverse) on one side only is a program one compiler
// builds and the other refuses.
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

// TestSelfHostCapabilityProfilesMatch compares the capability sets themselves,
// per profile. The target NAMES differ until #6635 lands, but a profile is the
// same object on both sides — which host grants what — so a capability added
// to a profile here and not there changes what one compiler will build.
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
				// The empty profile ("none") is a fall-through with no
				// literal to find, which is the same set by another spelling.
				continue
			}
			t.Errorf("profile %q is missing from the self-host's capability_profile()", name)
			continue
		}
		w := append([]string(nil), want...)
		g := append([]string(nil), got...)
		sort.Strings(w)
		sort.Strings(g)
		if strings.Join(w, ",") != strings.Join(g, ",") {
			t.Errorf("profile %q: native grants [%s], the self-host grants [%s]",
				name, strings.Join(w, " "), strings.Join(g, " "))
		}
	}
	for name := range found {
		if _, ok := capabilityProfiles[name]; !ok {
			t.Errorf("the self-host declares profile %q, which does not exist here", name)
		}
	}
}
