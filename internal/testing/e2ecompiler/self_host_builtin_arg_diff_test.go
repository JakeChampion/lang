package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostBuiltinArgDifferentialX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	for _, tc := range builtinMistypedCalls(t) {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			got := diagLines(driverDiags(runCheckerDriver(t, cmd, tc.name)))
			want := diagLines(goCheckerDiags(t, dir, tc.src))
			if !equalStrings(got, want) {
				t.Errorf("%s: the two checkers disagree on this call's arguments.\nnative:    %s\nself-host: %s\nsrc: %s",
					tc.name, joinOrNone(want), joinOrNone(got), tc.src)
			}
			if len(want) == 0 {
				t.Errorf("%s: the Go checker reports nothing for a call whose every argument is "+
					"mistyped — the generated call is wrong, not the compilers\nsrc: %s", tc.name, tc.src)
			}
		})
	}
}

func builtinMistypedCalls(t *testing.T) []struct{ name, src string } {
	t.Helper()
	sigs := nativeBuiltinSigs(t)
	var out []struct{ name, src string }
	for _, name := range selfHostParameterisedBuiltins(t) {
		sig, ok := sigs[name]
		if !ok {
			t.Errorf("compiler/checker.fern gives %q parameter types and native declares no such "+
				"builtin — one of the two tables names something the other has never heard of", name)
			continue
		}
		if hasTypeParamParam(sig) {
			continue
		}
		args := make([]string, 0, len(sig.Params))
		for _, p := range sig.Params {
			args = append(args, mistypedFor(name, p))
		}
		if len(args) == 0 {
			args = append(args, `"x"`)
		}
		out = append(out, struct{ name, src string }{
			name: strings.TrimLeft(name, "_"),
			src:  fmt.Sprintf("function main(): i32 {\n    %s(%s);\n    return 0;\n}\n", name, strings.Join(args, ", ")),
		})
	}
	if len(out) == 0 {
		t.Fatal("no builtins generated — this test would pass on anything")
	}
	return out
}

func hasTypeParamParam(sig *ast.FuncType) bool {
	for _, p := range sig.Params {
		if _, ok := p.(ast.ParamType); ok {
			return true
		}
	}
	return false
}

// print, write and eprint take any Display value, so an integer is not
// mistyped for them. An array implements no Display.
func mistypedFor(name string, p ast.Type) string {
	if name == "print" || name == "write" || name == "eprint" {
		return "[1, 2]"
	}
	if _, isStr := p.(ast.StringType); isStr {
		return "1"
	}
	return `"x"`
}

func nativeBuiltinSigs(t *testing.T) map[string]*ast.FuncType {
	t.Helper()
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	out := map[string]*ast.FuncType{}
	for name, sig := range info.FuncSigs {
		if strings.HasPrefix(name, "__method_") {
			continue
		}
		out[name] = sig
	}
	return out
}

func selfHostParameterisedBuiltins(t *testing.T) []string {
	t.Helper()
	body := selfHostFileSection(t, "checker.fern",
		`(?s)function intrinsic_params\(.*?\n\}`)
	seen := map[string]bool{}
	rows := selfHostFileSection(t, "builtinsigs.fern", `(?s)function builtin_sigs\(\): string\[\] \{.*?\n\}`)
	for _, m := range sigRowNameRE.FindAllStringSubmatch(rows, -1) {
		if !strings.HasPrefix(m[1], "__method_") {
			seen[m[1]] = true
		}
	}
	harvest := func(src string) {
		for _, name := range selfHostDispatchNames(t, src) {
			seen[name] = true
		}
	}
	harvest(body)
	for _, m := range familyPredicateRE.FindAllStringSubmatch(body, -1) {
		harvest(selfHostFileSection(t, "checker.fern",
			`(?s)function `+m[1]+`\(name: string\): boolean \{.*?\n\}`))
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

var (
	builtinNameRE     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	familyPredicateRE = regexp.MustCompile(`(is_[a-z0-9_]+)\(name\)`)
	sigRowNameRE      = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)\(`)
)

func selfHostDispatchNames(t *testing.T, src string) []string {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse builtin dispatch: %v", err)
	}
	lists := map[string][]*ast.ArrayLit{}
	ast.WalkProgram(prog, func(node ast.Node) bool {
		if v, ok := node.(*ast.Var); ok {
			if list, ok := v.Init.(*ast.ArrayLit); ok {
				lists[v.Name] = append(lists[v.Name], list)
			}
		}
		return true
	})
	seen := map[string]bool{}
	add := func(value string) {
		if builtinNameRE.MatchString(value) {
			seen[value] = true
		}
	}
	addList := func(expr ast.Expr) {
		list, ok := expr.(*ast.ArrayLit)
		if id, named := expr.(*ast.Ident); named {
			candidates := lists[id.Name]
			if len(candidates) != 1 {
				t.Fatalf("builtin name list %q has no unique literal declaration; update the inventory's scope handling", id.Name)
			}
			list, ok = candidates[0], true
		}
		if !ok {
			t.Fatalf("builtin name membership does not use a literal array; update the inventory")
		}
		for _, elem := range list.Elems {
			name, ok := elem.(*ast.StringLit)
			if !ok {
				t.Fatalf("builtin name list contains a non-literal; update the inventory")
			}
			add(name.Value)
		}
	}
	ast.WalkProgram(prog, func(node ast.Node) bool {
		switch expr := node.(type) {
		case *ast.Binary:
			if value := dispatchComparedWith(expr, "name"); value != nil {
				if name, ok := value.(*ast.StringLit); ok {
					add(name.Value)
				}
			}
		case *ast.Call:
			field, ok := expr.Callee.(*ast.FieldAccess)
			if ok && field.Field == "has_str" && len(expr.Args) == 2 && dispatchIdent(expr.Args[1], "name") {
				addList(expr.Args[0])
			}
		case *ast.ForEach:
			ast.Walk(expr.Body, func(node ast.Node) bool {
				if test, ok := node.(*ast.Binary); ok && dispatchIdent(dispatchComparedWith(test, "name"), expr.Var) {
					addList(expr.Iter)
				}
				return true
			})
		}
		return true
	})
	var out []string
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func dispatchComparedWith(expr *ast.Binary, name string) ast.Expr {
	if expr.Op == "==" {
		if dispatchIdent(expr.Left, name) {
			return expr.Right
		}
		if dispatchIdent(expr.Right, name) {
			return expr.Left
		}
	}
	return nil
}

func dispatchIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

func TestSelfHostDispatchNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"comparisons", `if (name == "first" || "second" == name) { return true; }`, "first,second"},
		{"type-name", `if (name == "__tcp_close_unchecked") { t_struct("Socket"); return true; }`, "__tcp_close_unchecked"},
		{"literal-list", `return util.has_str(["__alloc", "__fd_close"], name);`, "__alloc,__fd_close"},
		{"named-list", `let names: string[] = ["lane_a", "lane_b"]; return util.has_str(names, name);`, "lane_a,lane_b"},
		{"loop-list", `let names: string[] = ["math_a", "math_b"]; for n in names { if (n == name) { return true; } }`, "math_a,math_b"},
		{"unrelated-data", `let types: string[] = ["Socket"]; if (other == "Reader") { return true; }`, ""},
		{"unrelated-shadowing", `if (true) { let types = ["Socket"]; } if (false) { let types = ["Reader"]; } return name == "kept";`, "kept"},
		{"unknown-builtin", `if (name == "not_declared") { return true; }`, "not_declared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "function probe(name: string): boolean { " + tc.body + " return false; }"
			if got := strings.Join(selfHostDispatchNames(t, src), ","); got != tc.want {
				t.Fatalf("names = %q, want %q", got, tc.want)
			}
		})
	}
}

func selfHostFileSection(t *testing.T, file, pattern string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := regexp.MustCompile(pattern).FindString(string(b))
	if body == "" {
		t.Fatalf("%s: the section this gate reads has been renamed or reshaped; point the pattern at "+
			"its new form rather than deleting the gate", file)
	}
	return body
}

func diagLines(ds []driverDiag) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.code+": "+d.msg)
	}
	sort.Strings(out)
	return out
}
