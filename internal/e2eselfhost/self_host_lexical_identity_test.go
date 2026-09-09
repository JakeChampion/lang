package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostLexicalIdentityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	cases := []struct{ name, src, want string }{
		{"initializer", `function f(x: i64): i64 { var x = x + 1; return x; }`, "x=0;x=1;|$binding$0$x;$binding$1$x;"},
		{"nested capture", `function f(n: i32): i32 { var call = (): i32 => { var inner = (): i32 => n; var n = 99; return inner(); }; return call(); }`, "n=0;call=1;inner=2;n=3;|$binding$0$n;$binding$2$inner;$binding$1$call;"},
		{"sibling scopes", `function f(n: i32): i32 { if (true) { var n = 1; print(n); } else { var n = 2; print(n); } return n; }`, "n=0;n=1;n=2;|print;$binding$1$n;print;$binding$2$n;$binding$0$n;"},
		{"loop scope", `function f(xs: i32[], x: i32): i32 { for x in xs { print(x); } return x; }`, "xs=0;x=1;x=2;|$binding$0$xs;print;$binding$2$x;$binding$1$x;"},
		{"recursive lambda", `function f(): i32 { var recur = (n: i32): i32 => { if (n == 0) { return 7; } return recur(n - 1); }; return recur(2); }`, "recur=0;n=1;|$binding$1$n;$binding$0$recur;$binding$1$n;$binding$0$recur;"},
		{"assignment identity", `function f(n: i32): i32 { n = n + 1; var n = 4; n = n + 2; return n; }`, "n=0;n=1;|=$binding$0$n;$binding$0$n;=$binding$1$n;$binding$1$n;$binding$1$n;"},
		{"tuple binding", `function f(): i32 { var (x, y) = (3, 4); return x + y; }`, "x=0;y=1;|$binding$0$x;$binding$1$y;"},
		{"pattern scope", `enum E { Full(i32), Empty } function f(e: E, x: i32): i32 { match(e) { Full(x) => { print(x); }, Empty => {} } return x; }`, "e=0;x=1;x=2;|$binding$0$e;print;$binding$2$x;$binding$1$x;"},
		{"receiver", `struct S { value: i32 } function (s: S) f(n: i32): i32 { return s.value + n; }`, "s=0;n=1;|$binding$0$s;$binding$1$n;"},
		{"defer binding", `function f(n: i32): i32 { defer print(n); var n = 9; return n; }`, "n=0;n=1;|print;$binding$0$n;$binding$1$n;"},
	}
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./astwalk";
import "./lexer";
import "./lexical";
import "./parser";
import "./util";
function read_expr(e: ast.Expr, out: string): string {
    if let ast.ExprIdent(i) = e { return out + i.name + ";"; }
    return out;
}
function read_stmt(st: ast.Stmt, out: string): string {
    if let ast.StmtAssign(a) = st { return out + "=" + a.target + ";"; }
    return out;
}
function reads(fd: parser.FuncDecl): string {
    var out = "";
    for st in fd.body { out = astwalk.fold_stmt_nodes(st, out, read_stmt, read_expr, astwalk.descend_all); }
    return out;
}
function inspect(source: string): string {
    var mod = parser.parse_module(lexer.tokenize(source));
    var fd = mod.funcs[0];
    var before = reads(fd);
    var resolved = lexical.resolve_func(fd);
    if (reads(fd) != before) { return "mutated input"; }
    var pi = 0;
    while (pi < fd.params.len()) {
        var original = fd.params[pi];
        var renamed = resolved.func.params[pi];
        if (original.type_name != renamed.type_name || original.own != renamed.own ||
            original.fn_ret != renamed.fn_ret || original.fn_param_types != renamed.fn_param_types) {
            return "changed parameter contract";
        }
        pi = pi + 1;
    }
    if (fd.line != resolved.func.line || fd.col != resolved.func.col ||
        fd.ret_type != resolved.func.ret_type || fd.fip != resolved.func.fip || fd.fbip != resolved.func.fbip) {
        return "changed declaration metadata";
    }
    var out = "";
    for b in resolved.bindings { out = out + b.name + "=" + util.i32_to_string(b.id) + ";"; }
    return out + "|" + reads(resolved.func);
}
function main(): i32 {
`)
	for _, tc := range cases {
		fmt.Fprintf(&src, "print(inspect(%s));\n", strconv.Quote(tc.src))
	}
	src.WriteString("return 0;\n}\n")
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexical.fern", "astwalk.fern", "parser.fern", "lexer.fern")
	if err := os.WriteFile(filepath.Join(dir, "lexical_test.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "lexical_test.fern", "lexical_test")
	interp := buildLangBinForInterp(t)
	oracle, oracleErr := exec.Command(interp, "-interp", filepath.Join(dir, "lexical_test.fern")).CombinedOutput()
	if oracleErr != nil {
		t.Fatalf("lexical interpreter: %v\n%s", oracleErr, oracle)
	}
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("lexical resolver: %v\n%s", err, out)
	}
	rows := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(rows) != len(cases) {
		t.Fatalf("got %d rows, want %d: %q", len(rows), len(cases), out)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rows[i] != tc.want {
				t.Errorf("got %q, want %q", rows[i], tc.want)
			}
		})
	}
	if string(out) != string(oracle) {
		t.Errorf("native/interpreter disagreement:\n%s", oracle)
	}
}
