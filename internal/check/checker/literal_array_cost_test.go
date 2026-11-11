package checker

import (
	"strings"
	"testing"
	"time"
)

// The check of an array literal reads each element a fixed number of times.
// A table of the standard library has many thousands of elements, so a
// check that reads each earlier element again for each element takes
// minutes.
func TestLargeArrayLiteralChecksInLinearTime(t *testing.T) {
	const n = 300000
	for name, src := range map[string]string{
		"integers":            `function main(): i32 { let xs: i32[] = [` + strings.Repeat("7, ", n) + `7]; return xs.len() - ` + "300001" + `; }`,
		"floats":              `function main(): i32 { let xs = [` + strings.Repeat("0.5, ", n) + `0.5]; return xs.len() - ` + "300001" + `; }`,
		"floats after a read": `function main(): i32 { let k = 1.5; let xs = [k, ` + strings.Repeat("0.5, ", n) + `0.5]; return xs.len() - ` + "300002" + `; }`,
	} {
		done := make(chan error, 1)
		go func() { done <- checkSource(t, src) }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(2 * time.Minute):
			t.Fatalf("%s: the check of a literal of %d elements did not end in 2 minutes", name, n)
		}
	}
}
