package compiler

import (
	"io/fs"
	"strings"
	"testing"
)

// The embedded sources are what `fern` builds the compiler from; the test
// drivers live in drivers/ and must not ship with them.
func TestSourcesHoldNoTestDrivers(t *testing.T) {
	names, err := fs.Glob(Sources, "*")
	if err != nil {
		t.Fatal(err)
	}
	var sawEntry bool
	for _, n := range names {
		if n == "fern.fern" {
			sawEntry = true
		}
		if strings.HasSuffix(n, "_run.fern") && n != "playground_run.fern" {
			t.Errorf("%s is embedded; test drivers belong in compiler/drivers/", n)
		}
	}
	if !sawEntry {
		t.Errorf("fern.fern is not embedded among %d files", len(names))
	}
}
