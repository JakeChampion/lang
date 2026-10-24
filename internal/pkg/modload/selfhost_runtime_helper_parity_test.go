package modload

import (
	"os"
	"regexp"
	"sort"
	"testing"
)

const selfHostFlattenSrc = "../../../compiler/flatten.fern"

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
	body := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(m[1], "")
	var out []string
	for _, lit := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1) {
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
	for _, probe := range []string{"map_new_impl", "__map_set_impl", "__mapiter_key_impl"} {
		if !isRuntimeHelperName(probe) {
			t.Errorf("the self-host keeps %q bare and this side mangles it", probe)
		}
	}
}

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

func TestRuntimeHelperExemptionIsNarrow(t *testing.T) {
	for _, probe := range []string{"map_new", "parse", "__fern_alloc", "mapper", "__mapped"} {
		if isRuntimeHelperName(probe) {
			t.Errorf("%q is exempt from module prefixing and should not be", probe)
		}
	}
}
