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
			args = append(args, mistypedFor(p))
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

func mistypedFor(p ast.Type) string {
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
	rows := selfHostFileSection(t, "checker.fern", `(?s)function builtin_sigs\(\): string\[\] \{.*?\n\}`)
	for _, m := range sigRowNameRE.FindAllStringSubmatch(rows, -1) {
		if !strings.HasPrefix(m[1], "__method_") {
			seen[m[1]] = true
		}
	}
	harvest := func(src string) {
		for _, m := range quotedNameRE.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
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
	quotedNameRE      = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`)
	familyPredicateRE = regexp.MustCompile(`(is_[a-z0-9_]+)\(name\)`)
	sigRowNameRE      = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)\(`)
)

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
