package checker

import (
	"os"
	"regexp"
	"testing"
)

// The scalar types and the intrinsics of @norc are the same in both checkers.
func TestNoRCListsMatchSelfHost(t *testing.T) {
	b, err := os.ReadFile("../../../compiler/norc.fern")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, c := range []struct {
		fn   string
		want map[string]bool
	}{{"scalar", noRCScalars}, {"intrinsics", noRCIntrinsics}} {
		body := regexp.MustCompile(`(?s)pub function ` + c.fn + `\([^)]*\): [a-z\[\]]+ \{(.*?)\n\}`).FindStringSubmatch(src)
		if body == nil {
			t.Fatalf("no %s() in compiler/norc.fern; the extraction pattern has gone stale", c.fn)
		}
		got := map[string]bool{}
		for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(body[1], -1) {
			got[m[1]] = true
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: %d names in the self-host, %d here", c.fn, len(got), len(c.want))
		}
		for k := range c.want {
			if !got[k] {
				t.Errorf("%s: %q is here but not in the self-host", c.fn, k)
			}
		}
	}
}
