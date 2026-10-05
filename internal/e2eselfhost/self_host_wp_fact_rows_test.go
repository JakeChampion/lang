package e2eselfhost

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestWpFactRowsCoversEveryFnSigsField keeps fnsigs.wp_fact_rows complete. The
// per-module cache keys fold those rows, and the function names FnSigs' columns
// one by one, so a column added to FnSigs and not to the rows would let a
// cached unit outlive the fact it was lowered with.
func TestWpFactRowsCoversEveryFnSigsField(t *testing.T) {
	b, err := os.ReadFile("../../examples/self_host/fnsigs.fern")
	if err != nil {
		t.Fatalf("read fnsigs.fern: %v", err)
	}
	src := string(b)
	st := regexp.MustCompile(`(?s)\npub struct FnSigs \{\n(.*?)\n\}\n`).FindStringSubmatch(src)
	if st == nil {
		t.Fatal("cannot find `pub struct FnSigs` in fnsigs.fern")
	}
	fn := regexp.MustCompile(`(?s)\n// wp_fact_rows .*?\npub function wp_fact_rows\(.*?\n\}\n`).FindString(src)
	if fn == "" {
		t.Fatal("cannot find wp_fact_rows and its comment in fnsigs.fern")
	}
	fields := regexp.MustCompile(`(?m)^\s+([a-z_0-9]+):`).FindAllStringSubmatch(st[1], -1)
	if len(fields) == 0 {
		t.Fatal("parsed no FnSigs fields; the pattern no longer matches the struct")
	}
	var missing []string
	for _, f := range fields {
		if !strings.Contains(fn, "b."+f[1]) {
			missing = append(missing, f[1])
		}
	}
	if len(missing) > 0 {
		t.Fatalf("wp_fact_rows does not fold these FnSigs fields: %s", strings.Join(missing, ", "))
	}
}
