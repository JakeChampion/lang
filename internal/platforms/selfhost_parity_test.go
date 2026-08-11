package platforms

import (
	"os"
	"regexp"
	"slices"
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
// Target NAMES are compared too since #6635 gave the self-host driver this
// package's `<isa>-<environment>` spellings; capability sets are compared per
// PROFILE, which is where the meaning lives.

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

// profileExceptions records, per profile, the capabilities the self-host
// grants and this package does not — deliberately, and with a reason.
//
// There is exactly one. `subprocess` is gated in both tables, but no Go
// backend lowers it, so native grants it nowhere: its table records a codegen
// limitation as a target property. The self-host's x86-64 and arm64 emitters
// DO lower it (`__fern_subprocess`; TestSelfHostArm64DarwinBuilds runs four
// spawn cases end-to-end), so its hosted profile grants what its artifacts can
// actually reach. The wasi profiles grant it on neither side.
//
// Listing it here rather than relaxing the comparison is the point: any OTHER
// difference, in either direction, still fails — and this entry dies the day
// native's backends lower it.
var profileExceptions = map[string]map[string]bool{
	"hosted-native": {"subprocess": true},
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
		var g []string
		for _, c := range got {
			if profileExceptions[name][c] {
				continue
			}
			g = append(g, c)
		}
		sort.Strings(w)
		sort.Strings(g)
		if strings.Join(w, ",") != strings.Join(g, ",") {
			t.Errorf("profile %q: native grants [%s], the self-host grants [%s] (documented exceptions aside)",
				name, strings.Join(w, " "), strings.Join(g, " "))
		}
		for c := range profileExceptions[name] {
			if !slices.Contains(got, c) {
				t.Errorf("profile %q: the self-host no longer grants %q, so its exception entry in profileExceptions is stale — delete it", name, c)
			}
		}
	}
	for name := range found {
		if _, ok := capabilityProfiles[name]; !ok {
			t.Errorf("the self-host declares profile %q, which does not exist here", name)
		}
	}
}

// targetExceptions records the targets this package declares that the
// self-host does not. There is exactly one: the proxy world has no self-host
// counterpart at all — no component wrapper, no handler entry — which is
// #6636, not a naming difference. Listing it keeps every OTHER missing target
// a failure.
var targetExceptions = map[string]bool{"wasm32-wasi-http": true}

// TestSelfHostTargetNamesMatch pins the target list itself. The two compilers
// spelled targets differently until #6635 — `arm64` there, `arm64-linux` here
// — which meant a build command could not be moved between them, and a
// differential had to translate. The one difference the self-host is allowed
// is a target it has no backend for at all; there is none today, so the lists
// are compared whole.
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
		if targetExceptions[name] {
			continue
		}
		t.Errorf("target %q exists here but not in the self-host's targets()", name)
	}
	for name := range targetExceptions {
		if slices.Contains(got, name) {
			t.Errorf("the self-host now declares %q, so its entry in targetExceptions is stale — delete it", name)
		}
	}
	for _, name := range got {
		if !slices.Contains(want, name) {
			t.Errorf("the self-host declares target %q, which does not exist here", name)
		}
	}
}
