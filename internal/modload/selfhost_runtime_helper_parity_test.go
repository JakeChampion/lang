package modload

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

// `isRuntimeHelperName` above and `is_runtime_helper_name` in
// examples/self_host/flatten.fern are the same rule in two compilers, and the
// rule only works if they agree: `core/map`'s surface is declared as concrete
// `_impl` functions because the language has no generic method on a generic
// struct, every backend routes `map_new` / `__method_Map_get` onto them through
// ONE alias table (ir.CodegenAliases), and a table cannot name a function two
// ways. Before #9608 they did not agree — the self-host mangled the helpers to
// `map____map_*` and native kept them bare — which is what stood between the
// self-host and core/map's hash table.
//
// This reads the Fern predicate as data, so a row added on one side and not the
// other fails a fast Go test instead of surfacing as a dangling label in a
// backend. It builds nothing, for the same reason the caps and platforms parity
// tests build nothing.

const selfHostFlattenSrc = "../../examples/self_host/flatten.fern"

// selfHostHelperRows pulls the string literals out of the Fern predicate's
// body — its `map_new_impl` exact match and its two prefixes.
func selfHostHelperRows(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(selfHostFlattenSrc)
	if err != nil {
		t.Fatalf("reading %s: %v", selfHostFlattenSrc, err)
	}
	m := regexp.MustCompile(`(?s)pub function is_runtime_helper_name\(name: string\): boolean \{(.*?)\n\}`).
		FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no is_runtime_helper_name body found in %s — the extraction pattern has gone stale, "+
			"which would make this test vacuous", selfHostFlattenSrc)
	}
	var out []string
	for _, lit := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, lit[1])
	}
	sort.Strings(out)
	return out
}

func TestSelfHostRuntimeHelperRowsMatch(t *testing.T) {
	got := selfHostHelperRows(t)
	want := []string{"__map_", "__mapiter_", "map_new_impl"}
	if len(got) != len(want) {
		t.Fatalf("self-host predicate names %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("self-host predicate names %v, want %v", got, want)
		}
	}
	// And each row is one this side exempts too, or the symbol the alias table
	// names exists under only one compiler.
	for _, probe := range []string{"map_new_impl", "__map_set_impl", "__mapiter_key_impl"} {
		if !isRuntimeHelperName(probe) {
			t.Errorf("the self-host keeps %q bare and this side mangles it", probe)
		}
	}
}

// The one row this side has and the self-host deliberately does not. The
// self-host resolves an auto-discovered `__method_Array_<f>` by SUFFIX over
// both the bare and the `<mod>__`-mangled spelling, so exempting it there would
// move every stdlib method's symbol and buy nothing for Map. Asserted rather
// than left as prose, so closing the gap is a deliberate edit here and not a
// surprise.
func TestSelfHostRuntimeHelperMethodRowIsNativeOnly(t *testing.T) {
	if !isRuntimeHelperName("__method_Array_len") {
		t.Fatal("this side stopped exempting __method_*; the self-host parity note above is now stale")
	}
	for _, row := range selfHostHelperRows(t) {
		if row == "__method_" {
			t.Fatal("the self-host now exempts __method_* too — delete this test and the note in " +
				selfHostFlattenSrc + " rather than leaving both")
		}
	}
}

// Neither side exempts an ordinary name, or module prefixing would stop
// working altogether.
func TestRuntimeHelperExemptionIsNarrow(t *testing.T) {
	for _, probe := range []string{"map_new", "parse", "__fern_alloc", "mapper", "__mapped"} {
		if isRuntimeHelperName(probe) {
			t.Errorf("%q is exempt from module prefixing and should not be", probe)
		}
	}
}
