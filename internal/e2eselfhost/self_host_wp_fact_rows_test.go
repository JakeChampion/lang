package e2eselfhost

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestWpFactRowsCoversEveryFnSigsField keeps irlower.wp_fact_rows complete. The
// per-module cache keys fold those rows, and the function names FnSigs' columns
// one by one, so a column added to FnSigs and to neither the rows nor the
// signature-only list in its comment would let a cached unit outlive the fact it
// was lowered with.
func TestWpFactRowsCoversEveryFnSigsField(t *testing.T) {
	b, err := os.ReadFile("../../examples/self_host/irlower.fern")
	if err != nil {
		t.Fatalf("read irlower.fern: %v", err)
	}
	src := string(b)
	st := regexp.MustCompile(`(?s)\npub struct FnSigs \{\n(.*?)\n\}\n`).FindStringSubmatch(src)
	if st == nil {
		t.Fatal("cannot find `pub struct FnSigs` in irlower.fern")
	}
	fn := regexp.MustCompile(`(?s)\n// wp_fact_rows .*?\npub function wp_fact_rows\(.*?\n\}\n`).FindString(src)
	if fn == "" {
		t.Fatal("cannot find wp_fact_rows and its comment in irlower.fern")
	}
	fields := regexp.MustCompile(`(?m)^\s+([a-z_0-9]+):`).FindAllStringSubmatch(st[1], -1)
	if len(fields) < 40 {
		t.Fatalf("parsed only %d FnSigs fields; the pattern no longer matches the struct", len(fields))
	}
	var missing []string
	for _, f := range fields {
		if !strings.Contains(fn, "b."+f[1]) {
			missing = append(missing, f[1])
		}
	}
	if len(missing) > 0 {
		t.Fatalf("wp_fact_rows neither folds nor lists as signature-only these FnSigs fields: %s", strings.Join(missing, ", "))
	}
}
