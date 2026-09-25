package e2eselfhost

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// enumKeysDriver prints the name of every enum left after the parser's
// monomorphisation passes, one per line.
const enumKeysDriver = `import "./parser";
import "./rundriver";

function main(): i32 {
    var m: parser.Module = parser.module_with_builtins(rundriver.parse_stdin("enum_keys"));
    for e in m.enums { print(e.name + "\n"); }
    return 0;
}
`

// A generic enum at a tuple argument is cloned under native's mangle for the
// tuple (`tup`, then each element, an array element suffixed `_arr`). A tuple
// holding a generic enum has no clone name, so its enum stays generic.
func TestSelfHostGenericEnumTupleKey(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	if err := os.WriteFile(filepath.Join(dir, "enum_keys.fern"), []byte(enumKeysDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := buildSelfHostBin(t, gcc, dir, "enum_keys.fern", "enum_keys")

	const decl = "enum Opt[T] { Sm(T), Nn }\n"
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"annotated", `function main(): i32 { var o: Opt[(i32, i32)] = Sm((3, 4)); match (o) { Sm(p) => { return p.0; }, Nn => { return 0; } } }`,
			[]string{"Opt__tup_i32_i32"}},
		{"inferred", `function main(): i32 { var o = Sm((3, 4)); match (o) { Sm(p) => { return p.1; }, Nn => { return 0; } } }`,
			[]string{"Opt__tup_i32_i32"}},
		{"nested-and-array", `function main(): i32 { var o: Opt[(i32[], (i32, string))] = Nn; match (o) { Sm(p) => { return 1; }, Nn => { return 0; } } }`,
			[]string{"Opt__tup_i32_arr_tup_i32_string"}},
		{"simple-and-tuple", `function main(): i32 { var a: Opt[i32] = Sm(3); var b: Opt[(i32, i32)] = Nn; match (a) { Sm(n) => { return n; }, Nn => { return 0; } } }`,
			[]string{"Opt__i32", "Opt__tup_i32_i32"}},
		{"tuple-of-a-generic-enum", `function main(): i32 { var o: Opt[(Opt[i32], i32)] = Nn; match (o) { Sm(p) => { return 1; }, Nn => { return 0; } } }`,
			[]string{"Opt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Fields(string(runCapture(t, gcc, runner, driver, []byte(decl+tc.body))))
			sort.Strings(got)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("enums after monomorphisation = %v, want %v", got, tc.want)
			}
		})
	}
}
