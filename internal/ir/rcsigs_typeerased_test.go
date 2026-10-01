package ir

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The type-erased table names parameters of functions defined in
// core/map.fern; a rename there would leave the table pointing at nothing
// and the certify walk reading the parameter as consumed again.
func TestRcTypeErasedParamsNameMapHelpersThatExist(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "stdlib", "core", "map.fern"))
	if err != nil {
		t.Fatal(err)
	}
	for name, params := range rcTypeErasedParams {
		m := regexp.MustCompile(`(?m)^function ` + regexp.QuoteMeta(name) + `\(([^)]*)\)`).FindSubmatch(src)
		if m == nil {
			t.Errorf("%s: not defined in core/map.fern", name)
			continue
		}
		declared := regexp.MustCompile(`\w+: usize`).FindAll(m[1], -1)
		for i, reason := range params {
			if i >= len(declared) {
				t.Errorf("%s: parameter %d (%s) is past its %d usize parameters", name, i, reason, len(declared))
			}
			if r, ok := RcParamTypeErased(name, i); !ok || r != reason {
				t.Errorf("%s: RcParamTypeErased(%d) = %q, %v", name, i, r, ok)
			}
		}
	}
	if _, ok := RcParamTypeErased("__map_dec_value", 0); ok {
		t.Error("the buffer argument is borrowed, not type-erased")
	}
}
