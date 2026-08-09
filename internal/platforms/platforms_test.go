package platforms

import (
	"sort"
	"testing"
)

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
