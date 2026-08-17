package checker

import (
	"strings"
	"testing"
)

// TestMapLitEntriesMustMatchInferredTypes covers the E045 same-type rule
// on a map literal's entries: the first entry fixes K and V, and every
// later entry must match.
//
// The check compared each entry against itself. postSettleType returns
// its `prior` argument unchanged for anything that is not a numeric
// literal, and the loop passed the ALREADY-INFERRED key/value type as
// prior — so the comparison was `keyType != keyType` and no mismatch
// could ever be reported.
func TestMapLitEntriesMustMatchInferredTypes(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"key type mismatch",
			`import "core/map";
function main(): i32 { var m = Map { 1: "a", "two": "b" }; return 0; }`,
			"map key type string, expected i32",
		},
		{
			"value type mismatch",
			`import "core/map";
function main(): i32 { var m = Map { 1: "a", 2: 42 }; return 0; }`,
			"map value type i32, expected string",
		},
		{
			"struct value type mismatch",
			`import "core/map";
struct P { x: i32 }
struct Q { y: i32 }
function main(): i32 { var m = Map { 1: P { x: 1 }, 2: Q { y: 2 } }; return 0; }`,
			"map value type Q, expected P",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected E045, got a clean check for:\n%s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want %q, got: %v", tc.want, err)
			}
		})
	}

	// Homogeneous literals stay clean, including the mixed-width numeric
	// keys the settling pass is there to reconcile.
	for _, src := range []string{
		`import "core/map";
function main(): i32 { var m = Map { 1: "a", 2: "b" }; return m.len(); }`,
		`import "core/map";
function main(): i32 { var m: Map[i64, i32] = Map { 1 as i64: 10, 2 as i64: 20 }; return m.len(); }`,
		`import "core/map";
function main(): i32 { var m = Map { "a": 1, "b": 2 }; return m.len(); }`,
	} {
		if err := checkModuleSource(t, src); err != nil {
			t.Errorf("expected a clean check for:\n%s\ngot: %v", src, err)
		}
	}
}
