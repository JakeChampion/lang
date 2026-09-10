package checker_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// The self-host's `ow_fresh_owners` (examples/self_host/checker.fern) derives
// the fresh-owner set for USER functions from the module it is checking, the
// same way native's `isOwnedExpr` does. The BUILTIN half it cannot derive:
// builtins carry no declaration in the module, so the self-host hard-codes the
// names that pass native's rule — a pointer result and no pointer parameter,
// so the result cannot be a handed-back borrow of an argument.
//
// Nothing tied that list to the table it mirrors. A builtin gaining a pointer
// result, or losing its last pointer parameter, silently makes the self-host
// reject `f(builtin())` at an `own` parameter with an E051 native accepts —
// a diagnostic that exists in one compiler and not the other.
func TestSelfHostFreshOwnedBuiltinsMatchChecker(t *testing.T) {
	fern := parseFernFreshOwnedBuiltins(t, "../../examples/self_host/checker.fern")
	native := nativeFreshOwnedBuiltins(t)

	// Guard the gate itself: an anchor that moved, or a table that stopped
	// registering builtins, would otherwise compare two empty sets and pass.
	if len(fern) < 8 {
		t.Fatalf("only %d builtin names parsed out of ow_fresh_owners (%v) — has the list moved or been reshaped?", len(fern), sorted(fern))
	}
	if len(native) < 8 {
		t.Fatalf("only %d fresh-owning builtins computed from the checker's table (%v) — has the predicate or the table moved?", len(native), sorted(native))
	}

	for _, name := range sorted(native) {
		if !fern[name] {
			t.Errorf("%s has a pointer result and no pointer parameter, so native's isOwnedExpr treats its call as a fresh owner, but ow_fresh_owners in examples/self_host/checker.fern does not list it: the self-host reports E051 where native accepts", name)
		}
	}
	for _, name := range sorted(fern) {
		if !native[name] {
			t.Errorf("ow_fresh_owners in examples/self_host/checker.fern lists %s, but the checker's builtin table gives it no pointer result or a pointer parameter, so native's isOwnedExpr does not treat its call as a fresh owner: the self-host accepts where native reports E051", name)
		}
	}
}

// parseFernFreshOwnedBuiltins reads the hard-coded `var out: string[] = [ ... ]`
// literal at the head of ow_fresh_owners.
func parseFernFreshOwnedBuiltins(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fn := regexp.MustCompile(`(?s)function ow_fresh_owners\(.*?\n\}`).FindString(string(src))
	if fn == "" {
		t.Fatalf("no ow_fresh_owners function found in %s", path)
	}
	lit := regexp.MustCompile(`(?s)var out: string\[\] = \[(.*?)\];`).FindStringSubmatch(fn)
	if lit == nil {
		t.Fatalf("no `var out: string[] = [...]` literal found in ow_fresh_owners")
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(lit[1], -1) {
		out[m[1]] = true
	}
	return out
}

// nativeFreshOwnedBuiltins recomputes isOwnedExpr's rule over the checker's
// builtin signature table: a pointer result, and no pointer parameter. (The
// rule's second arm — every pointer parameter declared `own` — cannot fire for
// a builtin: none of them takes an `own` parameter.)
//
// Method builtins are excluded: they are reached through a receiver, which is
// a pointer argument the self-host's rule never sees, and ow_fresh_owners
// likewise skips every function with a receiver.
func nativeFreshOwnedBuiltins(t *testing.T) map[string]bool {
	t.Helper()
	const probe = `function main(): i32 { return 0; }`
	prog, err := parser.Parse(probe)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := checker.Check(prog)
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
		if sig == nil || sig.Result == nil || !ast.IsPointerType(sig.Result) {
			continue
		}
		anyPtrParam := false
		for _, pt := range sig.Params {
			if pt != nil && ast.IsPointerType(pt) {
				anyPtrParam = true
			}
		}
		if !anyPtrParam {
			out[name] = true
		}
	}
	return out
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
