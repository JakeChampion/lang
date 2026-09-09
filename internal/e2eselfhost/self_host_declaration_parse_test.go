package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostDeclarationParse(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cases := []struct{ name, spelling, tag, params, result, dyn string }{
		{"grouped", "(((i64, f32) => u64))", "fn", "i64,f32", "u64", ""},
		{"named parameters", "((x: i64, y: str) => void)", "fn", "i64,str", "void", ""},
		{"generic and dyn", "((Map[string, i64], dyn Shape, i32) => void)", "fn", "Map[string, i64],dyn Shape,i32", "void", "1"},
		{"array", "((((str) => i64)))[]", "fn[]", "str", "i64", ""},
		{"callable parameter", "(((i64) => i64) => i64)", "fn", "(i64) => i64", "i64", ""},
		{"callable result", "(i32) => (i64) => string", "fn", "i32", "(i64) => string", ""},
		{"zero", "(() => void)", "fn", "", "void", ""},
	}
	var src strings.Builder
	src.WriteString("import \"./lexer\"; import \"./parser\"; import \"./ast\";\nfunction main(): i32 {\n")
	for i, tc := range cases {
		program := fmt.Sprintf(`struct Holder { cb: %[1]s }
enum Task { First(%[1]s), Later(i32, %[1]s), Named { cb: %[1]s } }
function probe(cb: %[1]s): %[1]s { var f: %[1]s = cb; return f; }`, tc.spelling)
		fmt.Fprintf(&src, "var m%d = parser.parse_module(lexer.tokenize(%s));\n", i, strconv.Quote(program))
		fmt.Fprintf(&src, "var p%d = m%d.funcs[0].params[0]; print(p%d.type_name + \"|\" + p%d.fn_param_types + \"|\" + p%d.fn_ret + \"|\" + p%d.fn_param_dyn);\n", i, i, i, i, i, i)
		fmt.Fprintf(&src, "var f%d = m%d.funcs[0]; print(f%d.ret_type + \"|\" + f%d.ret_fn_param_types + \"|\" + f%d.ret_fn_ret);\n", i, i, i, i, i)
		fmt.Fprintf(&src, "match (f%d.body[0]) { ast.StmtVar(v) => { print(v.type_name + \"|\" + v.fn_param_types + \"|\" + v.fn_ret); }, _ => { return 90; } }\n", i)
		fmt.Fprintf(&src, "for s in m%d.structs { if (s.name == \"Holder\" || s.enum_owner == \"Task\") { var f = s.fields[s.fields.len() - 1]; print(f.type_name + \"|\" + f.fn_param_types + \"|\" + f.fn_ret); } }\n", i)
	}
	src.WriteString("return 0; }\n")
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "parser.fern")
	if err := os.WriteFile(filepath.Join(dir, "decl_parse.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "decl_parse.fern", "decl_parse")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("parse declaration contracts: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	const sites = 7
	if len(lines) != sites*len(cases) {
		t.Fatalf("got %d rows, want %d:\n%s", len(lines), sites*len(cases), out)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.tag + "|" + tc.params + "|" + tc.result
			for j := 0; j < sites; j++ {
				expected := want
				if j == 0 {
					expected += "|" + tc.dyn
				}
				if got := lines[i*sites+j]; got != expected {
					t.Errorf("site %d = %q, want %q", j, got, expected)
				}
			}
		})
	}
}
