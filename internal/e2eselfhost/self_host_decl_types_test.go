package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostDeclTypesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cases := []struct{ name, expr, want string }{
		// The noncallable declaration adapter erases view spellings, while
		// callable sidecars below retain their full signature.
		{"partial enum capture", `decltypes.param_from_type("capture", partial())`, "Result[string, $unbound]||"},
		{"nested partial enum capture", `decltypes.param_from_type("capture", typeinfo.TypeArray { elem: partial(), view: false })`, "Result[string, $unbound][]||"},
		{"partial callable capture", `tp([partial()], partial())`, "fn|Result[str, $unbound]|Result[str, $unbound]"},
		{"unrelated unknown enum argument", `decltypes.param_from_type("capture", typeinfo.TypeUnion { name: "Result", args: [view(), typeinfo.unchecked()] })`, "||"},
		{"absent argument outside enum", `decltypes.param_from_type("capture", typeinfo.TypeUnknown { reason: "unbound T" })`, "||"},
		{"scalar widths", `sp("(i64, f32) => u64")`, "fn|i64,f32|u64"},
		{"grouped signature", `sp("((str) => Box[str])")`, "fn|str|Box[str]"},
		{"nested grouping", `sp("((((str) => Box[str])))")`, "fn|str|Box[str]"},
		{"zero parameters", `sp("() => i32")`, "fn||i32"},
		{"return array", `sp("(str) => i32[]")`, "fn|str|i32[]"},
		{"return nested array", `sp("() => Box[str][][]")`, "fn||Box[str][][]"},
		{"array of functions", `sp("((str) => i32)[]")`, "||"},
		{"grouped array of functions", `sp("(((str) => i32)[])")`, "||"},
		{"function parameter", `sp("((str) => f32, Map[str, i64]) => (u8, str)")`, "fn|(str) => f32,Map[str, i64]|(u8, str)"},
		{"return function", `sp("(str) => () => i64[]")`, "fn|str|() => i64[]"},
		{"dyn parameters", `sp("(dyn A, i64, dyn B + C) => boolean")`, "fn|dyn A,i64,dyn B + C|boolean"},
		{"tuple is not callable", `sp("(i32, str)")`, "||"},
		{"opaque is not signature", `sp("fn")`, "||"},
		{"missing result", `sp("() => ")`, "||"},
		{"typed signature", `tp([view(), fraction()], box())`, "fn|str,f32|Box[str]"},
		{"typed zero parameters", `tp([], fraction())`, "fn||f32"},
		{"typed nested function", `tp([callback([view()], fraction())], callback([], box()))`, "fn|((str) => f32)|(() => Box[str])"},
		{"typed return array", `tp([], typeinfo.TypeArray { elem: view(), view: false })`, "fn||str[]"},
		{"typed dyn", `tp([typeinfo.TypeDyn { traits: "A" }, fraction(), typeinfo.TypeDyn { traits: "B + C" }], view())`, "fn|dyn A,f32,dyn B + C|str"},
		{"unknown parameter", `tp([typeinfo.unchecked()], view())`, "||"},
		{"unknown result", `tp([], typeinfo.unchecked())`, "||"},
		{"unknown nested parameter", `tp([callback([typeinfo.unchecked()], view())], view())`, "||"},
		{"opaque typed function", `decltypes.fn_param_from_type("capture", typeinfo.TypeFunc { param_types: [], param_own: [], ret_type: view(), params_known: false })`, "||"},
		{"nonfunction typed binder", `decltypes.fn_param_from_type("capture", view())`, "||"},
	}
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./decltypes";
import "./typeinfo";
function view(): typeinfo.Type { return typeinfo.TypeString { tag: 1 }; }
function partial(): typeinfo.Type { return typeinfo.TypeUnion { name: "Result", args: [view(), typeinfo.TypeUnknown { reason: "unbound E" }] }; }
function fraction(): typeinfo.Type { return typeinfo.TypeFloat { width: 32, polymorphic: false }; }
function box(): typeinfo.Type { return typeinfo.TypeStruct { name: "Box", args: [view()] }; }
function callback(params: typeinfo.Type[], ret: typeinfo.Type): typeinfo.Type { return typeinfo.TypeFunc { param_types: params, param_own: [], ret_type: ret, params_known: true }; }
function tp(params: typeinfo.Type[], ret: typeinfo.Type): ast.ParamDecl { return decltypes.fn_param_from_type("capture", callback(params, ret)); }
function sp(s: string): ast.ParamDecl { return decltypes.fn_param_from_spelling("capture", s); }
function main(): i32 {
`)
	for i, tc := range cases {
		fmt.Fprintf(&src, "let p%d = %s;\nprint(p%d.type_name + \"|\" + p%d.fn_param_types + \"|\" + p%d.fn_ret);\n", i, tc.expr, i, i, i)
		fmt.Fprintf(&src, "if (p%d.name != %s || p%d.own || p%d.has_default) { return %d; }\n", i, strconv.Quote("capture"), i, i, i+1)
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "decltypes.fern")
	if err := os.WriteFile(filepath.Join(dir, "decl_types.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "decl_types.fern", "decl_types")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("declaration metadata: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("got %d rows, want %d: %q", len(lines), len(cases), out)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if lines[i] != tc.want {
				t.Fatalf("metadata = %q, want %q", lines[i], tc.want)
			}
		})
	}
}
