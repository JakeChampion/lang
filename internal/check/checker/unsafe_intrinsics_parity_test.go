package checker

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The self-host checker and this checker must refuse the same raw builtins
// outside an unsafe block, or a program passes one oracle and fails the other.
func TestUnsafeIntrinsicsMatchSelfHost(t *testing.T) {
	const path = "../../../compiler/checker.fern"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	m := regexp.MustCompile(`(?s)function unsafe_intrinsics\(\): string\[\] \{\s*return \[(.*?)\];`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s no longer defines unsafe_intrinsics() as one array literal", path)
	}
	selfHost := map[string]bool{}
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		selfHost[q[1]] = true
	}
	if len(selfHost) == 0 {
		t.Fatalf("the unsafe_intrinsics() literal in %s holds no names", path)
	}
	var onlyGo, onlySelfHost []string
	for name := range UnsafeIntrinsics {
		if !selfHost[name] {
			onlyGo = append(onlyGo, name)
		}
	}
	for name := range selfHost {
		if !UnsafeIntrinsics[name] {
			onlySelfHost = append(onlySelfHost, name)
		}
	}
	sort.Strings(onlyGo)
	sort.Strings(onlySelfHost)
	if len(onlyGo) > 0 || len(onlySelfHost) > 0 {
		t.Fatalf("the unsafe builtin sets differ\nonly in internal/check/checker: %s\nonly in %s: %s",
			strings.Join(onlyGo, ", "), path, strings.Join(onlySelfHost, ", "))
	}
	for name := range UnsafeIntrinsics {
		if !strings.HasPrefix(name, "__") {
			t.Errorf("%q is not a builtin name", name)
		}
	}
}
