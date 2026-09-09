package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostDeclarationSpellingContracts(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cases := []struct{ name, spelling, want string }{
		{"scalar widths", "(i64, f32) => u64", "fn|i64,f32|u64|"},
		{"grouped signature", "((str) => Box[str])", "fn|str|Box[str]|"},
		{"nested grouping", "((((str) => Box[str])))", "fn|str|Box[str]|"},
		{"zero parameters", "() => i32", "fn||i32|"},
		{"return array", "(str) => i32[]", "fn|str|i32[]|"},
		{"return nested array", "() => Box[str][][]", "fn||Box[str][][]|"},
		{"array of functions", "((str) => i32)[]", "|||"},
		{"grouped array of functions", "(((str) => i32)[])", "|||"},
		{"function parameter", "((str) => f32, Map[str, i64]) => (u8, str)", "fn|(str) => f32,Map[str, i64]|(u8, str)|"},
		{"return function", "(str) => () => i64[]", "fn|str|() => i64[]|"},
		{"dyn positions", "(dyn A, i64, dyn B + C) => boolean", "fn|dyn A,i64,dyn B + C|boolean|0,2"},
		{"tuple is not callable", "(i32, str)", "|||"},
		{"opaque is not signature", "fn", "|||"},
		{"missing result", "() => ", "|||"},
	}
	var src strings.Builder
	src.WriteString("import \"./irlower\";\nfunction main(): i32 {\n")
	for i, tc := range cases {
		fmt.Fprintf(&src, "var p%d = irlower.fn_param_from_spelling(\"capture\", %s);\n", i, strconv.Quote(tc.spelling))
		fmt.Fprintf(&src, "print(p%d.type_name + \"|\" + p%d.fn_param_types + \"|\" + p%d.fn_ret + \"|\" + p%d.fn_param_dyn);\n", i, i, i, i)
		fmt.Fprintf(&src, "if (p%d.name != \"capture\" || p%d.own || p%d.has_default || p%d.ret_arr) { return %d; }\n", i, i, i, i, i+1)
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "irlower.fern")
	if err := os.WriteFile(filepath.Join(dir, "declaration_contract.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "declaration_contract.fern", "declaration_contract")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("declaration contract: %v\n%s", err, out)
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
