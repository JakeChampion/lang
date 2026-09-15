package e2eselfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var identifierCountCases = []struct {
	name, body, ident string
	want              int
}{
	{"absent", "return 0;", "x", 0},
	{"duplicates", "return x + x;", "x", 2},
	{"assignment target", "x = x + 1; return x;", "x", 3},
	{"declaration is not a read", "var x = 1; return x;", "x", 1},
	{"loop reads", "while (x > 0) { x = x - 1; } return 0;", "x", 3},
	{"branches", "if (flag) { return x; } else { return x + x; }", "x", 3},
	{"array and index", "return [x, x][x];", "x", 3},
	{"tuple binders", "var (x, y) = pair; return x + y;", "x", 1},
	{"lambda parameter shadows", "var f = (x: i32): i32 => { return x; }; return f(1);", "x", 0},
	{"lambda free reads", "var f = (): i32 => { return x + x; }; return f();", "x", 2},
	{"lambda initializer reads outer", "var f = (): i32 => { var x = x + 1; return x; }; return f();", "x", 1},
	{"lambda local hides later reads", "var f = (): i32 => { var x = 1; return x; }; return f();", "x", 0},
	{"nested lambda binding", "var f = (): i32 => { var x = 1; var g = (): i32 => { return x; }; return g(); }; return f();", "x", 0},
}

// Both the independently pinned counts and the existing list collector must
// agree. The expression API is checked against its collector on each node.
func identifierCountSource() string {
	var src strings.Builder
	src.WriteString(`import "./ast";
import "./astwalk";
import "./lexer";
import "./parser";
import "./util";
function frequency(ids: string[], name: string): i32 {
    var n: i32 = 0;
    for id in ids { if (id == name) { n = n + 1; } }
    return n;
}
function probe(source: string, name: string): i32 {
    var mod = parser.parse_module(lexer.tokenize(source));
    if (mod.funcs.len() != 1 || mod.funcs[0].body.len() == 0) { return -1; }
    var body = mod.funcs[0].body;
    var ids: string[] = [];
    for st in body { ids = astwalk.collect_idents_stmt(st, ids); }
    var count: i32 = astwalk.count_ident_stmts(body, name);
    if (count != frequency(ids, name)) { return -2; }
    function check_expr(e: ast.Expr, own bad: i32): i32 {
        if (astwalk.count_ident_expr(e, name) != frequency(astwalk.collect_idents_expr(e, []), name)) { return bad + 1; }
        return bad;
    }
    var bad: i32 = 0;
    for st in body { bad = astwalk.fold_stmt(st, bad, check_expr); }
    if (bad != 0) { return -3; }
    return count;
}
function main(): i32 {
`)
	for _, tc := range identifierCountCases {
		fixture := "function fixture(): i32 { " + tc.body + " }"
		fmt.Fprintf(&src, "print(util.i32_to_string(probe(%s, %s)));\n", strconv.Quote(fixture), strconv.Quote(tc.ident))
	}
	src.WriteString("return 0;\n}\n")
	return src.String()
}

func checkIdentifierCountOutput(t *testing.T, out []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("identifier counts: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(identifierCountCases) {
		t.Fatalf("got %d rows, want %d: %q", len(lines), len(identifierCountCases), out)
	}
	for i, tc := range identifierCountCases {
		t.Run(tc.name, func(t *testing.T) {
			if lines[i] != strconv.Itoa(tc.want) {
				t.Errorf("count = %s, want %d", lines[i], tc.want)
			}
		})
	}
}

func TestSelfHostIdentifierCountsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "astwalk.fern", "parser.fern", "lexer.fern")
	if err := os.WriteFile(filepath.Join(dir, "identifier_counts.fern"), []byte(identifierCountSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := buildSelfHostBin(t, gcc, dir, "identifier_counts.fern", "identifier_counts")
	out, err := runX86_64Bin(runner, bin).CombinedOutput()
	checkIdentifierCountOutput(t, out, err)
}
