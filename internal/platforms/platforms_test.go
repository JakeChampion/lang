package platforms

import (
	"sort"
	"strings"
	"testing"
)

// Every listed target names an environment that exists, so ForTarget
// composes rather than panicking. This is the invariant that replaces
// "every target spells out its own capability list".
func TestEveryTargetComposes(t *testing.T) {
	for _, name := range Targets() {
		entry, ok := table[name]
		if !ok {
			t.Errorf("Targets() lists %q with no table entry", name)
			continue
		}
		if _, ok := environments[entry.environment]; !ok {
			t.Errorf("target %q names unknown environment %q", name, entry.environment)
		}
		if d := ForTarget(name); d == nil || d.Name != name {
			t.Errorf("ForTarget(%q) did not compose", name)
		}
	}
}

// No two environments may carry the same capability set. Two that do
// are one environment wearing two names — exactly the state the four
// native targets were in before they were collapsed, where adding a
// capability meant editing the identical list four times and missing
// one was silent.
func TestEnvironmentsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for name, env := range environments {
		key := strings.Join(env.capabilities, ",")
		if prev, dup := seen[key]; dup {
			t.Errorf("environments %q and %q have identical capability sets — collapse them", prev, name)
		}
		seen[key] = name
	}
}

// TestForTargetCoversEveryCanonicalTarget — every -target=
// value cmd/fern accepts must have a Descriptor entry. The
// canonical-set list is duplicated here as a sentinel; adding
// a new target to cmd/fern's flag dispatch without also
// landing a descriptor here surfaces immediately rather than
// at the user-visible "platform has no capabilities" surface.
func TestForTargetCoversEveryCanonicalTarget(t *testing.T) {
	canonical := []string{"arm64", "arm64-android", "arm64-darwin", "x86-64", "wasm", "wasi-http"}
	for _, name := range canonical {
		t.Run(name, func(t *testing.T) {
			d := ForTarget(name)
			if d == nil {
				t.Fatalf("ForTarget(%q) returned nil — descriptor missing", name)
			}
			if d.Name != name {
				t.Errorf("descriptor.Name = %q, want %q", d.Name, name)
			}
			if d.Description == "" {
				t.Errorf("descriptor %q has empty Description", name)
			}
			if len(d.HandlerKinds) == 0 {
				t.Errorf("descriptor %q has no HandlerKinds (need at least one entry point)", name)
			}
		})
	}
}

// TestForTargetUnknownReturnsNil — the lookup is the canonical
// "is this a real target?" check. Tests / driver code that
// passes a typo or stale name relies on the nil return to
// reject cleanly.
func TestForTargetUnknownReturnsNil(t *testing.T) {
	if d := ForTarget("mars"); d != nil {
		t.Errorf("ForTarget(\"mars\") = %v, want nil", d)
	}
	if d := ForTarget(""); d != nil {
		t.Errorf("ForTarget(\"\") = %v, want nil", d)
	}
}

// TestTargetsIsStable — sort order is part of the contract:
// `fern -targets` and any LSP-side completion of -target needs
// reproducible output across runs.
func TestTargetsIsStable(t *testing.T) {
	a := Targets()
	b := Targets()
	if len(a) != len(b) {
		t.Fatalf("Targets() returned different lengths: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("position %d: %q vs %q", i, a[i], b[i])
		}
	}
	if !sort.StringsAreSorted(a) {
		t.Errorf("Targets() not sorted: %v", a)
	}
}

// TestHasCapabilityHonoursDescriptor — `now` is everywhere
// (every target has clock access); `fetch` only lands on
// wasi-http; `kv` is on nothing yet.
func TestHasCapabilityHonoursDescriptor(t *testing.T) {
	cases := []struct {
		target string
		cap    string
		want   bool
	}{
		// Universal capabilities.
		{"arm64", "now", true},
		{"x86-64", "now", true},
		{"wasi-http", "now", true},
		{"wasm", "now", true},
		// `fetch` is wasi-http-only today.
		{"wasi-http", "fetch", true},
		{"arm64", "fetch", false},
		{"wasm", "fetch", false},
		// `kv` doesn't exist anywhere yet.
		{"wasi-http", "kv", false},
		{"arm64", "kv", false},
		// Unknown target returns false (not panic).
		{"mars", "log", false},
	}
	for _, tc := range cases {
		got := HasCapability(tc.target, tc.cap)
		if got != tc.want {
			t.Errorf("HasCapability(%q, %q) = %v, want %v", tc.target, tc.cap, got, tc.want)
		}
	}
}

// TestDescriptorStringIsHumanReadable — `fern -targets`
// surfaces this; pin the shape so accidental reformatting
// surfaces here rather than as a UX change.
func TestDescriptorStringIsHumanReadable(t *testing.T) {
	d := ForTarget("wasi-http")
	if d == nil {
		t.Fatal("ForTarget(\"wasi-http\") returned nil")
	}
	got := d.String()
	want := "wasi-http: WebAssembly Component Model — proxy world (wasi:http/incoming-handler)."
	if got != want {
		t.Errorf("String() = %q\n want %q", got, want)
	}
}

// TestEveryTargetDeclaresAnEntryShape — the entry shape drives whether
// the native backends emit `_start` and the argc/argv/envp capture
// (#6510), so an environment that forgets to declare one would silently
// get the zero value. OrDefault resolves that to EntryProcess, which is
// the right fallback for a hosted target and exactly the wrong one for a
// hostless target: it would emit an entry point that reads a process
// stack no kernel populated. Fail here instead.
func TestEveryTargetDeclaresAnEntryShape(t *testing.T) {
	valid := map[EntryShape]bool{EntryProcess: true, EntryExports: true, EntryReset: true}
	for _, name := range Targets() {
		switch e := ForTarget(name).Entry; {
		case e == "":
			t.Errorf("target %q declares no entry shape; its environment needs one", name)
		case !valid[e]:
			t.Errorf("target %q declares unknown entry shape %q", name, e)
		}
	}
}

// TestFreestandingIsEnteredByNobody pins the guest shape as freestanding's
// answer today. EntryReset — the artifact owning the machine — is the
// second value the descriptor is shaped to hold rather than a rewrite;
// docs/BARE-METAL-PLAN.md.
func TestFreestandingIsEnteredByNobody(t *testing.T) {
	if got := ForTarget("freestanding").Entry; got != EntryExports {
		t.Errorf("freestanding entry = %q, want %q: nothing enters it, its embedder calls in", got, EntryExports)
	}
}

// TestEntryShapeOrDefault pins the zero-value resolution the codegen
// Options rely on, so an `Options{}` literal keeps emitting `_start`.
func TestEntryShapeOrDefault(t *testing.T) {
	if got := EntryShape("").OrDefault(); got != EntryProcess {
		t.Errorf("zero EntryShape resolved to %q, want %q", got, EntryProcess)
	}
	for _, e := range []EntryShape{EntryProcess, EntryExports, EntryReset} {
		if got := e.OrDefault(); got != e {
			t.Errorf("%q.OrDefault() = %q, want it unchanged", e, got)
		}
	}
}

// TestNoTargetMissesLogCapability — `log` is the absolute
// minimum capability every HOSTED target needs to surface
// error output. Tests that we don't accidentally ship a
// target with an empty Capabilities list.
//
// A freestanding target is the deliberate exception, and the
// only one: it has no host to log to, and an empty capability
// set is its entire point (#6509). Keying the exemption on
// NoBackend rather than the name means the day something
// emits for it, this invariant applies again — at which point
// "where does a freestanding artifact put a panic message?"
// is a question with a real answer instead of an omission.
func TestNoTargetMissesLogCapability(t *testing.T) {
	for _, name := range Targets() {
		d := ForTarget(name)
		if d.NoBackend {
			continue
		}
		if !HasCapability(name, "log") {
			t.Errorf("target %q is missing the `log` capability: %v", name, d.Capabilities)
		}
	}
}
