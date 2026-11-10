package checker

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestSelfHostFreshOwnedBuiltinsMatchChecker(t *testing.T) {
	fern := parseFernFreshOwnedBuiltins(t, "../../../compiler/checker.fern")
	native := nativeFreshOwnedBuiltins(t)

	if len(fern) < 8 {
		t.Fatalf("only %d builtin names parsed out of ow_fresh_builtins (%v) — has the list moved or been reshaped?", len(fern), sortedNames(fern))
	}
	if len(native) < 8 {
		t.Fatalf("only %d fresh-owning builtins computed from the checker's table (%v) — has the predicate or the table moved?", len(native), sortedNames(native))
	}

	for _, name := range sortedNames(native) {
		if !fern[name] {
			t.Errorf("%s has an owned result in the Go builtin contract but is absent from ow_fresh_builtins", name)
		}
	}
	for _, name := range sortedNames(fern) {
		if !native[name] {
			t.Errorf("ow_fresh_builtins lists %s, but its Go builtin contract does not guarantee an owned result", name)
		}
	}
}

func parseFernFreshOwnedBuiltins(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fn := regexp.MustCompile(`(?s)function ow_fresh_builtins\(.*?\n\}`).FindString(string(src))
	if fn == "" {
		t.Fatalf("no ow_fresh_builtins function found in %s", path)
	}
	lit := regexp.MustCompile(`(?s)let out: string\[\] = \[(.*?)\];`).FindStringSubmatch(fn)
	if lit == nil {
		t.Fatalf("no `let out: string[] = [...]` literal found in ow_fresh_builtins")
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(lit[1], -1) {
		out[m[1]] = true
	}
	return out
}

func nativeFreshOwnedBuiltins(t *testing.T) map[string]bool {
	t.Helper()
	const probe = `function main(): i32 { return 0; }`
	prog, err := parser.Parse(probe)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	declared := map[string]bool{}
	for _, fn := range prog.Funcs {
		declared[fn.Name] = true
	}
	out := map[string]bool{}
	for name, sig := range info.FuncSigs {
		if declared[name] || strings.HasPrefix(name, "__method_") {
			continue
		}
		if sig == nil || sig.Result == nil || !ast.IsPointerType(sig.Result) && !(mayRetainBorrow(sig.Result) && freshResultBuiltins[name]) {
			continue
		}
		anyPtrParam := false
		for _, pt := range sig.Params {
			if pt != nil && mayRetainBorrow(pt) {
				anyPtrParam = true
			}
		}
		if !anyPtrParam || freshResultBuiltins[name] {
			out[name] = true
		}
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
