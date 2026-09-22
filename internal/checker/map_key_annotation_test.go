package checker

import (
	"strings"
	"testing"
)

// mapKeyAnnotationCases are the WRITTEN `Map[K, V]` positions, one per row.
// `want` is the substring of the expected E045, or "" for a program that must
// check clean.
//
// Only the map-literal paths validated a key type, so every one of the
// rejecting rows below was accepted and reached codegen — where a float key
// segfaulted the self-host (#9973) and a tuple key silently answered the
// wrong value (#10009).
var mapKeyAnnotationCases = []struct{ name, src, want string }{
	{
		"var annotation",
		`import "core/map";
function main(): i32 { var m: Map[f64, i32] = map_new(2); return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"parameter",
		`import "core/map";
function take(m: Map[f64, i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"return type",
		`import "core/map";
function make(): Map[f64, i32] { return map_new(2); }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"struct field",
		`import "core/map";
struct S { m: Map[f64, i32] }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"enum payload",
		`import "core/map";
enum E { A, B(Map[f64, i32]) }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"nested in an array",
		`import "core/map";
function take(ms: Map[f64, i32][]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"nested in a tuple",
		`import "core/map";
function take(p: (string, Map[f64, i32])): i32 { return 0; }
function main(): i32 { return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"empty literal with an annotation",
		`import "core/map";
function main(): i32 { var m: Map[f64, i32] = Map {}; return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		// A tuple key type-checked and then silently answered the wrong
		// value: inserting at (1, 2) and reading the same pair back gave
		// the default, not 7.
		"tuple key",
		`import "core/map";
function take(m: Map[(i32, i32), i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"map key type (i32, i32) is not yet supported",
	},
	{
		"struct key without the derives",
		`import "core/map";
struct K { a: i32 }
function take(m: Map[K, i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"a struct used as a key must derive Eq and Hash",
	},
	{
		"struct key with the derives",
		`import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32 }
function take(m: Map[K, i32]): i32 { return m.len(); }
function main(): i32 { var m: Map[K, i32] = map_new(2); return take(m); }`,
		"",
	},
	{
		"narrow integer and string keys",
		`import "core/map";
function take(a: Map[u8, i32], b: Map[string, i32], c: Map[u32, i32]): i32 { return 0; }
function main(): i32 { var m: Map[u8, i32] = map_new(2); return take(m, map_new(1), map_new(1)); }`,
		"",
	},
	{
		// A type parameter is accepted here and re-checked once it is
		// instantiated, exactly as the literal path has always treated one.
		"generic key parameter",
		`import "core/map";
function take[T](m: Map[T, i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"",
	},
}

// TestMapKeyTypeIsCheckedWhereverItIsWritten pins E045 on every position a
// `Map[K, V]` can be written, not just the literal spelling.
func TestMapKeyTypeIsCheckedWhereverItIsWritten(t *testing.T) {
	for _, tc := range mapKeyAnnotationCases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected a clean check, got: %v\nsrc:\n%s", err, tc.src)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected E045, got a clean check for:\n%s", tc.src)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a diagnostic containing %q, got: %v\nsrc:\n%s", tc.want, err, tc.src)
			}
		})
	}
}

// TestMapKeyTypeIsReportedOnce pins that the annotation check and the
// map-literal check do not both fire on one program. The literal reports at
// the offending key, which is the better anchor, so the annotation stands
// down — but only when the literal has a key to report at. `Map {}` has none,
// and before that distinction every derived-struct-key program in the
// repository lost its diagnostic.
func TestMapKeyTypeIsReportedOnce(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{
			"annotation and a non-empty literal",
			`import "core/map";
function main(): i32 { var m: Map[f64, i32] = Map { 1.5: 7 }; return 0; }`,
		},
		{
			"annotation and an empty literal",
			`import "core/map";
function main(): i32 { var m: Map[f64, i32] = Map {}; return 0; }`,
		},
		{
			"annotation alone",
			`import "core/map";
function main(): i32 { var m: Map[f64, i32] = map_new(2); return 0; }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected E045, got a clean check for:\n%s", tc.src)
			}
			if n := strings.Count(err.Error(), "map key type"); n != 1 {
				t.Fatalf("want exactly one map-key diagnostic, got %d:\n%v\nsrc:\n%s", n, err, tc.src)
			}
		})
	}
}
