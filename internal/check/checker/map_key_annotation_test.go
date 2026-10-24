package checker

import (
	"strings"
	"testing"
)

var mapKeyAnnotationCases = []struct{ name, src, want string }{
	{
		"let annotation",
		`import "core/map";
function main(): i32 { let m: Map[f64, i32] = map_new(2); return 0; }`,
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
function main(): i32 { let m: Map[f64, i32] = Map {}; return 0; }`,
		"map key type f64 is not yet supported",
	},
	{
		"tuple key",
		`import "core/map";
function take(m: Map[(i32, i32), i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"",
	},
	{
		"array key",
		`import "core/map";
function take(m: Map[i32[], i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"",
	},
	{
		"tuple key in a literal",
		`import "core/map";
function main(): i32 { let m = Map { (1, "a"): 5 }; return m.len(); }`,
		"",
	},
	{
		"nested tuple and array key",
		`import "core/map";
function take(m: Map[(boolean, string[], (u8, char)), i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"",
	},
	{
		"tuple key holding a struct",
		`import "core/map";
struct S { a: i32 }
function take(m: Map[(i32, S), i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"a tuple or array used as a key may hold only",
	},
	{
		"float array key",
		`import "core/map";
function take(m: Map[f64[], i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"a tuple or array used as a key may hold only",
	},
	{
		"boolean key",
		`import "core/map";
function main(): i32 {
    let m: Map[boolean, i32] = map_new(8);
    m = m.insert(true, 5);
    return m.get_or(true, 0);
}`,
		"",
	},
	{
		"boolean key in a literal",
		`import "core/map";
function main(): i32 { let m = Map { true: 1 }; return m.len(); }`,
		"",
	},
	{
		"char key",
		`import "core/map";
function main(): i32 {
    let m: Map[char, i32] = map_new(8);
    m = m.insert('a', 5);
    return m.get_or('a', 0);
}`,
		"",
	},
	{
		"char key in a literal",
		`import "core/map";
function main(): i32 { let m = Map { 'a': 1, 'b': 2 }; return m.len(); }`,
		"",
	},
	{
		"borrowed string key",
		`import "core/map";
function main(): i32 {
    let m: Map[str, i32] = map_new(8);
    m = m.insert("ab", 5);
    return m.get_or("ab", 0);
}`,
		"",
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
function main(): i32 { let m: Map[K, i32] = map_new(2); return take(m); }`,
		"",
	},
	{
		"narrow integer and string keys",
		`import "core/map";
function take(a: Map[u8, i32], b: Map[string, i32], c: Map[u32, i32]): i32 { return 0; }
function main(): i32 { let m: Map[u8, i32] = map_new(2); return take(m, map_new(1), map_new(1)); }`,
		"",
	},
	{
		"generic key parameter",
		`import "core/map";
function take[T](m: Map[T, i32]): i32 { return 0; }
function main(): i32 { return 0; }`,
		"",
	},
}

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

func TestMapKeyTypeIsReportedOnce(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{
			"annotation and a non-empty literal",
			`import "core/map";
function main(): i32 { let m: Map[f64, i32] = Map { 1.5: 7 }; return 0; }`,
		},
		{
			"annotation and an empty literal",
			`import "core/map";
function main(): i32 { let m: Map[f64, i32] = Map {}; return 0; }`,
		},
		{
			"annotation alone",
			`import "core/map";
function main(): i32 { let m: Map[f64, i32] = map_new(2); return 0; }`,
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
