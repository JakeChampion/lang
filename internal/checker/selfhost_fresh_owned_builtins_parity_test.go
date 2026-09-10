package checker

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
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
		t.Fatalf("only %d builtin names parsed out of ow_fresh_owners (%v) — has the list moved or been reshaped?", len(fern), sortedNames(fern))
	}
	if len(native) < 8 {
		t.Fatalf("only %d fresh-owning builtins computed from the checker's table (%v) — has the predicate or the table moved?", len(native), sortedNames(native))
	}

	for _, name := range sortedNames(native) {
		if !fern[name] {
			t.Errorf("%s has a pointer result and no pointer parameter, so native's isOwnedExpr treats its call as a fresh owner, but ow_fresh_owners in examples/self_host/checker.fern does not list it: the self-host reports E051 where native accepts", name)
		}
	}
	for _, name := range sortedNames(fern) {
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
// builtin signature table: a pointer result, and no pointer parameter.
//
// This covers the FuncSigs-derived half of isOwnedExpr only. The rule's second
// arm (every pointer parameter declared `own`) is vacuous for builtins today
// because ownFuncs holds user functions alone; nothing here would notice if
// that changed. Its freshOwnedProducers half is pinned separately, below.
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

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The self-host mirrors native's freshOwnedProducers table — the calls whose
// result is fresh by contract rather than by signature — as a hard-coded arm in
// `ow_is_owned_expr` keyed on the method's SPELLING, because the self-host does
// not mangle at parse time. Native keys the mangled `__method_<type>_<method>`.
//
// Nothing tied the two together. A producer added to native's table, or dropped
// from it, would leave the self-host admitting a call native refuses at an `own`
// parameter, or refusing one it accepts — the same E051 divergence in the other
// direction.
func TestSelfHostFreshOwnedProducersMatchChecker(t *testing.T) {
	fern := parseFernFreshOwnedProducers(t, "../../examples/self_host/checker.fern")

	if len(fern) == 0 {
		t.Fatalf("no `mfa.field == \"...\"` producer arm parsed out of ow_is_owned_expr — has it moved or been reshaped?")
	}
	if len(freshOwnedProducers) == 0 {
		t.Fatalf("freshOwnedProducers is empty — has the table moved?")
	}

	// Native's key is mangled and the self-host's is not, so the two meet at
	// the method-name suffix.
	for mangled := range freshOwnedProducers {
		matched := false
		for spelling := range fern {
			if strings.HasSuffix(mangled, "_"+spelling) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("freshOwnedProducers credits %s as fresh by contract, but ow_is_owned_expr in examples/self_host/checker.fern admits no matching method spelling (%v): the self-host reports E051 where native accepts", mangled, sortedNames(fern))
		}
	}
	for spelling := range fern {
		matched := false
		for mangled := range freshOwnedProducers {
			if strings.HasSuffix(mangled, "_"+spelling) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("ow_is_owned_expr in examples/self_host/checker.fern admits `.%s()` as fresh by contract, but freshOwnedProducers has no such entry: the self-host accepts where native reports E051", spelling)
		}
	}
}

// parseFernFreshOwnedProducers reads the method spellings ow_is_owned_expr
// admits as fresh-by-contract producers.
func parseFernFreshOwnedProducers(t *testing.T, path string) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fn := regexp.MustCompile(`(?s)function ow_is_owned_expr\(.*?\n\}`).FindString(string(src))
	if fn == "" {
		t.Fatalf("no ow_is_owned_expr function found in %s", path)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`mfa\.field == "([^"]+)"`).FindAllStringSubmatch(fn, -1) {
		out[m[1]] = true
	}
	return out
}
