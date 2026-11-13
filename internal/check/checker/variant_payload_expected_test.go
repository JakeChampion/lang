package checker

import (
	"strings"
	"testing"
)

// A variant payload's declared type is the expected type of its argument, as
// a let annotation is, so an empty literal settles through a generic call.
func TestVariantPayloadIsTheArgumentsExpectedType(t *testing.T) {
	const decls = `import "core/map";
function pick[T](cond: boolean, a: T, b: T): T { return if (cond) { a } else { b }; }
enum E { M(Map[i32, i32]), N(i64) }
`
	good := map[string]string{
		"empty map through a generic call": `function main(): i32 { match (M(pick(true, Map {}, Map {}))) { M(m) => { return m.len(); }, N(_) => { return 1; } } }`,
		"nested generic calls":             `function main(): i32 { let e: E = M(pick(false, Map {}, pick(true, Map {}, Map {}))); match (e) { M(m) => { return m.len(); }, N(_) => { return 1; } } }`,
		"wide literal":                     `function main(): i32 { let e: E = N(pick(true, 5000000000, 1)); match (e) { M(_) => { return 1; }, N(v) => { return (v / 1000000000) as i32; } } }`,
	}
	for name, src := range good {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]struct{ src, want string }{
		"wrong map type": {`function main(): i32 { let m: Map[i32, string] = Map {}; let e: E = M(pick(true, m, m)); return 0; }`,
			"payload 0 type"},
		"wrong payload": {`function main(): i32 { let e: E = N(pick(true, "a", "b")); return 0; }`,
			"payload 0 type"},
	}
	for name, c := range bad {
		err := checkSource(t, decls+c.src)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not say %q", name, err.Error(), c.want)
		}
	}
}
