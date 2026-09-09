package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostLexicalCaptureTypesX86_64(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"wide shadow", `function f(n: i64): i32 { var cb = (): i64 => { var answer = n; var n = 99; return answer; }; return 0; }`, "n:i64;"},
		{"tuple binder", `function f(): i32 { var (n, other) = (7, 8); var cb = (): i32 => n; return 0; }`, "n:i32;"},
		{"pattern binder", `enum E { Full(i32), Empty } function f(e: E): i32 { match(e) { Full(n) => { var cb = (): i32 => n; }, Empty => {} } return 0; }`, "n:i32;"},
		{"guarded binder", `enum E { Full(i32), Empty } function f(): i32 { match(E.Full(7)) { Full(n) when n == 7 => { var cb = (): i32 => n; }, _ => {} } return 0; }`, "n:i32;"},
		{"callable and view", `function f(callback: (f32, str) => i64, text: str): i32 { var cb = (): i32 => { callback(1.0f32, text); return 0; }; return 0; }`, "callback:((f32, str) => i64);text:str;"},
		{"opaque shadows global", `function f[T](opaque: T): i32 { var cb = (): i32 => { opaque; return 0; }; return 0; } function opaque(): i32 { return 1; }`, "opaque:unknown;"},
		{"recursive binding", `function f(recur: str, outside: i64): i32 { var recur = (n: i32): i32 => { outside; return recur(n - 1); }; return 0; }`, "outside:i64;"},
		{"global is not capture", `function f(): i32 { var cb = (): i32 => global(); return 0; } function global(): i32 { return 7; }`, ""},
	}
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./astwalk";
import "./checker";
import "./lexer";
import "./lexical";
import "./parser";
import "./typeinfo";
function add_lambda(e: ast.Expr, out: ast.ExprLambda[]): ast.ExprLambda[] {
    if let ast.ExprLambda(lm) = e { return out.append(lm); }
    return out;
}
function lambdas(fd: parser.FuncDecl): ast.ExprLambda[] {
    var out: ast.ExprLambda[] = [];
    for st in fd.body { out = astwalk.fold_stmt(st, out, add_lambda); }
    return out;
}
function spelling(ty: typeinfo.Type): string {
    if let typeinfo.TypeUnknown(u) = ty { return "unknown"; }
    return typeinfo.spelling(ty);
}
function report(caps: ast.TypedBinding[]): string {
    var out = "";
    for cap in caps { out = out + cap.name + ":" + spelling(cap.ty) + ";"; }
    return out;
}
function inspect(source: string): string {
    var raw = parser.parse_module(lexer.tokenize(source));
    var checked = checker.annotate_module(raw);
    var original = lambdas(raw.funcs[0]);
    var annotated = lambdas(checked.funcs[0]);
    if (original.len() != 1 || annotated.len() != 1) { return "wrong lambda count"; }
    if (original[0].captures_known || !annotated[0].captures_known) { return "annotation state"; }
    var before = report(annotated[0].captures);
    var resolved = lexical.resolve_func(checked.funcs[0]);
    var renamed = lambdas(resolved.func);
    if (renamed.len() != 1 || !renamed[0].captures_known) { return "lost lambda"; }
    if (renamed[0].captures.len() != annotated[0].captures.len()) { return "lost capture"; }
    var i = 0;
    while (i < renamed[0].captures.len()) {
        var a = annotated[0].captures[i];
        var b = renamed[0].captures[i];
        if (spelling(a.ty) != spelling(b.ty)) { return "changed type"; }
        var found = false;
        for binding in resolved.bindings {
            if (binding.name == a.name && binding.symbol == b.name) { found = true; }
        }
        if (!found) { return "capture lost declaration identity"; }
        i = i + 1;
    }
    if (before != report(annotated[0].captures)) { return "mutated checked input"; }
    return before;
}
function main(): i32 {
`)
	for _, tc := range cases {
		fmt.Fprintf(&src, "print(inspect(%s));\n", strconv.Quote(tc.src))
	}
	src.WriteString("return 0;\n}\n")
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "checker.fern", "lexical.fern")
	if err := os.WriteFile(filepath.Join(dir, "capture_types.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "capture_types.fern", "capture_types")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("capture contracts: %v\n%s", err, out)
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
}
