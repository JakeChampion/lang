package ir

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The type-erased table names the value parameter `v` of functions defined
// in core/map.fern; a rename or a reordering there would leave the table
// poisoning the wrong word and the certify walk reading the value as
// consumed again.
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
		declared := regexp.MustCompile(`(\w+): \w+`).FindAllSubmatch(m[1], -1)
		for i, reason := range params {
			if i >= len(declared) {
				t.Errorf("%s: parameter %d (%s) is past its %d parameters", name, i, reason, len(declared))
				continue
			}
			if got := string(declared[i][1]); got != "v" {
				t.Errorf("%s: parameter %d is %s, not the value parameter v", name, i, got)
			}
		}
	}
	if _, ok := RcParamTypeErased("__map_dec_value", 0); ok {
		t.Error("the buffer argument is borrowed, not type-erased")
	}
}
