package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostLambdaScopeWalkX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cases := []struct{ name, params, body, want string }{
		{"initializer reads outer", "", "var n = n + 1; return n;", "n;"},
		{"read before local", "", "consume(n); var n = 1; return n;", "consume;n;"},
		{"branch does not leak", "", "if (flag) { var x = 1; } return x;", "flag;x;"},
		{"loop binder does not leak", "", "for x in xs { consume(x); } return x;", "xs;consume;x;"},
		{"nested closure before local", "", "var f = (): i32 => { return n; }; var n = 1; return f();", "n;"},
		{"nested closure after local", "", "var n = 1; var f = (): i32 => { return n; }; return f();", ""},
		{"parameters bind", "n: i32", "return n + outer;", "outer;"},
		{"nested parameters bind", "", "var f = (n: i32): i32 => { return n + outer; }; return f(1);", "outer;"},
		{"write before local", "", "n = 1; var n = 2; return n;", "n;"},
		{"write to local", "", "var n = 1; n = 2; return n;", ""},
		{"recursive local lambda", "", "var recur = (n: i32): i32 => { return recur(n - 1) + outer; }; return recur(2);", "outer;"},
		{"tuple binders", "", "var (x, y) = pair; return x + y;", "pair;"},
	}
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./astwalk";
import "./lexer";
import "./parser";
function refs(source: string): string {
    var mod = parser.parse_module(lexer.tokenize(source));
    if (mod.funcs.len() != 1) { return "bad function parse"; }
    if let ast.StmtVar(v) = mod.funcs[0].body[0] {
        if let ast.ExprLambda(lam) = v.init {
            var out: string = "";
            for name in astwalk.collect_idents_expr(v.init, []) { out = out + name + ";"; }
            return out;
        }
    }
    return "bad lambda parse";
}
function main(): i32 {
`)
	for _, tc := range cases {
		fixture := "function fixture(): i32 { var callback = (" + tc.params + "): i32 => { " + tc.body + " }; return 0; }"
		fmt.Fprintf(&src, "print(refs(%s));\n", strconv.Quote(fixture))
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "astwalk.fern", "parser.fern", "lexer.fern")
	if err := os.WriteFile(filepath.Join(dir, "lambda_scope.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "lambda_scope.fern", "lambda_scope")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("lambda scope walk: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("got %d rows, want %d: %q", len(lines), len(cases), out)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if lines[i] != tc.want {
				t.Fatalf("free variables = %q, want %q", lines[i], tc.want)
			}
		})
	}
}
