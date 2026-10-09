package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSelfHostCaptureContractRewritesX86_64(t *testing.T) {
	cases := []struct{ name, mode, source, want string }{
		{"elide first capture", "elide", `function f(n: i64, text: string): i32 { let cb = (): i32 => { assert(n > 0); return text.len(); }; return cb(); }`, "text:string;"},
		{"elide all captures", "elide", `function f(n: i64): i32 { let cb = (): i32 => { assert(n > 0); return 7; }; return cb(); }`, ""},
		{"elide keeps unknown", "elide", `function f[T](n: i64, opaque: T): i32 { let cb = (): i32 => { assert(n > 0); opaque; return 7; }; return cb(); }`, "opaque:unknown;"},
		{"cell keeps width", "box", `function f(): i64 { let n: i64 = 7i64; let cb = (): i64 => n; return cb(); }`, "$cell$n:Cell[i64];"},
		{"cell keeps callable", "box", `function f(): i64 { let n: () => i64 = source; let cb = (): i64 => n(); return cb(); } function source(): i64 { return 7i64; }`, "$cell$n:Cell[(() => i64)];"},
		{"substitution adds typed capture", "subst", `function f(n: f32, g: () => f32): f32 { let cb = (): f32 => g(); return cb(); }`, "n:f32;"},
		{"missing injected contract declines", "missing", `function f(n: f32, g: () => f32): f32 { let cb = (): f32 => g(); return cb(); }`, "g:(() => f32);"},
	}
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./astwalk";
import "./capturebox";
import "./callsubst";
import "./checker";
import "./constfold";
import "./lexer";
import "./parser";
import "./typeinfo";
function gather(e: ast.Expr, own out: ast.ExprLambda[]): ast.ExprLambda[] {
    if let ast.ExprLambda(lm) = e { return out.append(lm); }
    return out;
}
function report(body: ast.Stmt[]): string {
    let lambdas: ast.ExprLambda[] = [];
    for st in body { lambdas = astwalk.fold_stmt(st, lambdas, gather); }
    if (lambdas.len() != 1 || !lambdas[0].captures_known) { return "invalid lambda contract"; }
    let out = "";
    for cap in lambdas[0].captures {
        let ty = typeinfo.spelling(cap.ty);
        if let typeinfo.TypeUnknown(u) = cap.ty { ty = "unknown"; }
        out = out + cap.name + ":" + ty + ";";
    }
    return out;
}
function inspect(source: string, mode: string): string {
    let checked = checker.annotate_module(parser.parse_module(lexer.tokenize(source)));
    let body = checked.funcs[0].body;
    let before = report(body);
    let result: ast.Stmt[] = body;
    if (mode == "elide") { result = constfold.elide_asserts(checked).funcs[0].body; }
    if (mode == "box") {
        let sites: string[] = [];
        let types: string[] = [];
        for st in body {
            if let ast.StmtVar(v) = st {
                if (v.name == "n") {
                    sites = sites.append(capturebox.site_key(v.name, v.line, v.col));
                    types = types.append(v.type_name);
                }
            }
        }
        if (sites.len() != 1) { return "missing cell declaration"; }
        result = capturebox.box_rewrite_stmts(body, ["n"], types, sites, []);
    }
    if (mode == "subst" || mode == "missing") {
        let bindings: ast.TypedBinding[] = [];
        if (mode == "subst") { bindings = bindings.append(ast.TypedBinding { name: "n", ty: typeinfo.TypeFloat { width: 32, polymorphic: false } }); }
        let args: ast.Expr[] = [ast.ExprIdent { name: "n", line: 0, col: 0, ty: "f32" }];
        result = callsubst.subst_fcall_stmts(body, "g", "hoisted", args, bindings);
    }
    if (report(body) != before) { return "mutated checked input"; }
    return report(result);
}
function main(): i32 {
`)
	for _, tc := range cases {
		fmt.Fprintf(&src, "print(inspect(%s, %s));\n", strconv.Quote(tc.source), strconv.Quote(tc.mode))
	}
	src.WriteString("return 0;\n}\n")
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "checker.fern", "capturebox.fern", "callsubst.fern", "constfold.fern")
	if err := os.WriteFile(filepath.Join(dir, "capture_rewrites.fern"), []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "capture_rewrites.fern", "capture_rewrites")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil {
		t.Fatalf("capture rewrites: %v\n%s", err, out)
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
