package platforms

import (
	"sort"
	"strings"
	"testing"
)

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

func TestCapabilityProfilesAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for name, caps := range capabilityProfiles {
		key := strings.Join(caps, ",")
		if prev, dup := seen[key]; dup {
			t.Errorf("profiles %q and %q have identical capability sets — collapse them", prev, name)
		}
		seen[key] = name
	}
}

func TestTargetNameMatchesItsHalves(t *testing.T) {
	for _, name := range Targets() {
		d := ForTarget(name)
		if want := d.ISA + "-" + d.Environment; want != name {
			t.Errorf("target %q composes to %q — name and halves disagree", name, want)
		}
		if _, ok := capabilityProfiles[environments[d.Environment].profile]; !ok {
			t.Errorf("environment %q names unknown profile", d.Environment)
		}
	}
}

func TestForTargetCoversEveryCanonicalTarget(t *testing.T) {
	canonical := []string{"arm64-linux", "arm64-android", "arm64-darwin", "x86-64-linux", "wasm32-wasi", "wasm32-wasi-http"}
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

func TestForTargetUnknownReturnsNil(t *testing.T) {
	if d := ForTarget("mars"); d != nil {
		t.Errorf("ForTarget(\"mars\") = %v, want nil", d)
	}
	if d := ForTarget(""); d != nil {
		t.Errorf("ForTarget(\"\") = %v, want nil", d)
	}
}

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

func TestHasCapabilityHonoursDescriptor(t *testing.T) {
	cases := []struct {
		target string
		cap    string
		want   bool
	}{
		{"arm64-linux", "now", true},
		{"x86-64-linux", "now", true},
		{"wasm32-wasi-http", "now", true},
		{"wasm32-wasi", "now", true},
		{"wasm32-wasi-http", "fetch", true},
		{"arm64-linux", "fetch", false},
		{"wasm32-wasi", "fetch", false},
		{"wasm32-wasi-http", "kv", false},
		{"arm64-linux", "kv", false},
		{"mars", "log", false},
	}
	for _, tc := range cases {
		got := HasCapability(tc.target, tc.cap)
		if got != tc.want {
			t.Errorf("HasCapability(%q, %q) = %v, want %v", tc.target, tc.cap, got, tc.want)
		}
	}
}

func TestDescriptorStringIsHumanReadable(t *testing.T) {
	d := ForTarget("wasm32-wasi-http")
	if d == nil {
		t.Fatal("ForTarget(\"wasm32-wasi-http\") returned nil")
	}
	got := d.String()
	want := "wasm32-wasi-http: WebAssembly Component Model — proxy world (wasi:http/incoming-handler)."
	if got != want {
		t.Errorf("String() = %q\n want %q", got, want)
	}
}

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

func TestFreestandingIsEnteredByNobody(t *testing.T) {
	if got := ForTarget("arm64-freestanding").Entry; got != EntryExports {
		t.Errorf("freestanding entry = %q, want %q: nothing enters it, its embedder calls in", got, EntryExports)
	}
}

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
