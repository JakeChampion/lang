package checker

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// TestSelfHostRoutesEveryNativeBuiltin asks the question the name gate
// (TestSelfHostKnowsEveryNativeBuiltin) cannot: can the self-hosted compiler
// LOWER each builtin native offers? A builtin whose name every list carries
// but no lowering routes compiles through the checker and stops at the
// self-host's bail site. `__sum_bytes` (#9127), `buf_new` and `window_size`
// (#9124) each reached main that way, and the name gate passed on all three
// (#9158, #9156).
//
// A builtin is routed when a self-host lowering source names it: irlower for
// the AST path, semlower and semsource for the typed one. ir.fern's op table
// does not count, since an op nothing emits is the window_size shape. The
// check is string lookups over committed sources, so it runs on every host,
// including the ones where the e2e self-host legs skip.
func TestSelfHostRoutesEveryNativeBuiltin(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	routed := map[string]bool{}
	for _, f := range []string{"irlower.fern", "semlower.fern", "semsource.fern"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllStringSubmatch(string(b), -1) {
			routed[m[1]] = true
		}
	}
	var missing, nowRouted []string
	for name := range info.FuncSigs {
		if name == "main" || strings.HasPrefix(name, "__method_") || strings.Contains(name, "__assoc_") {
			continue
		}
		_, nativeOnly := nativeOnlyBuiltins[name]
		switch {
		case !routed[name] && !nativeOnly:
			missing = append(missing, name)
		case routed[name] && nativeOnly:
			nowRouted = append(nowRouted, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(nowRouted)
	if len(missing) > 0 {
		t.Errorf("%d builtin(s) native lowers that no self-host lowering routes: %s\n"+
			"A program calling one type-checks and then fails to compile on the self-host.",
			len(missing), strings.Join(missing, ", "))
	}
	if len(nowRouted) > 0 {
		t.Errorf("nativeOnlyBuiltins lists %s, which the self-host now routes: remove the row(s)",
			strings.Join(nowRouted, ", "))
	}
}

// nativeOnlyBuiltins is the builtins the self-host deliberately does not
// lower, each with its reason. The test fails when a listed name becomes
// routed, so the list only shrinks.
var nativeOnlyBuiltins = map[string]string{
	"__alloc_reuse": "native's Perceus reuse token, which only native's own reuse lowering emits",
	"__rc_get":      "an rc inspection intrinsic native's rc tests call; no self-host lowering handles it",
	"__c_call0":     ffiNotLowered,
	"__c_call0_f32": ffiNotLowered,
	"__c_call0_f64": ffiNotLowered,
	"__c_call1":     ffiNotLowered,
	"__c_call1_f32": ffiNotLowered,
	"__c_call1_f64": ffiNotLowered,
	"__c_call2":     ffiNotLowered,
	"__c_call2_f32": ffiNotLowered,
	"__c_call2_f64": ffiNotLowered,
	"__c_call3":     ffiNotLowered,
	"__c_call3_f32": ffiNotLowered,
	"__c_call3_f64": ffiNotLowered,
	"__c_call4":     ffiNotLowered,
	"__c_call4_f32": ffiNotLowered,
	"__c_call4_f64": ffiNotLowered,
}

const ffiNotLowered = "the C-ABI trampolines are not lowered on any self-host backend (#4375)"
