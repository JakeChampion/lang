package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostBuiltinSigsMatch(t *testing.T) {
	native := nativeBuiltinSigRows(t)
	selfHost := selfHostBuiltinSigRows(t)
	var missing, extra []string
	for name, row := range native {
		if got, ok := selfHost[name]; !ok || got != row {
			missing = append(missing, row)
		}
	}
	for name, row := range selfHost {
		if _, ok := native[name]; !ok {
			extra = append(extra, row)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%d native builtin signature(s) compiler/checker.fern's builtin_sigs() "+
			"lacks or spells differently; the rows native renders are:\n        \"%s\",",
			len(missing), strings.Join(missing, "\",\n        \""))
	}
	if len(extra) > 0 {
		t.Errorf("%d builtin_sigs() row(s) for a name native does not register: %s",
			len(extra), strings.Join(extra, "; "))
	}
}

func nativeBuiltinSigRows(t *testing.T) map[string]string {
	t.Helper()
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	names := nativeBuiltinNames(t)
	for name, sig := range info.FuncSigs {
		if strings.HasPrefix(name, "__method_") && !sigNamesTypeParam(sig) || strings.HasPrefix(name, "__c_call") {
			names = append(names, name)
		}
	}
	out := map[string]string{}
	for _, name := range names {
		sig := info.FuncSigs[name]
		var ps []string
		for _, p := range sig.Params {
			ps = append(ps, sigSpelling(p))
		}
		out[name] = name + "(" + strings.Join(ps, ", ") + "): " + sigSpelling(sig.Result)
	}
	return out
}

func sigNamesTypeParam(sig *ast.FuncType) bool {
	found := false
	var walk func(ast.Type)
	walk = func(t ast.Type) {
		switch x := t.(type) {
		case ast.ParamType:
			found = true
		case ast.ArrayType:
			walk(x.Elem)
		case ast.SliceType:
			walk(x.Elem)
		case ast.StructType:
			for _, a := range x.Args {
				walk(a)
			}
		case ast.EnumType:
			for _, a := range x.Args {
				walk(a)
			}
		case ast.TupleType:
			for _, e := range x.Elems {
				walk(e)
			}
		}
	}
	for _, p := range sig.Params {
		walk(p)
	}
	if sig.Result != nil {
		walk(sig.Result)
	}
	return found
}

func sigSpelling(t ast.Type) string {
	if t == nil {
		return "void"
	}
	return t.String()
}

var (
	selfHostBuiltinSigsRE = regexp.MustCompile(
		`(?s)function builtin_sigs\(\): string\[\] \{\s*return \[(.*?)\n\s*\];`)
	sigRowRE = regexp.MustCompile(`"([^"]*)"`)
)

func selfHostBuiltinSigRows(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "compiler", "checker.fern"))
	if err != nil {
		t.Fatalf("read self-host checker.fern: %v", err)
	}
	m := selfHostBuiltinSigsRE.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("cannot find builtin_sigs() in compiler/checker.fern — " +
			"the pattern no longer matches, so this test proves nothing")
	}
	out := map[string]string{}
	for _, q := range sigRowRE.FindAllStringSubmatch(m[1], -1) {
		row := q[1]
		out[row[:strings.Index(row, "(")]] = row
	}
	if len(out) == 0 {
		t.Fatal("builtin_sigs() parsed to an empty list — this test would fail on everything")
	}
	return out
}
