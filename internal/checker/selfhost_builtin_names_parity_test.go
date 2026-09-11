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

// TestSelfHostKnowsEveryNativeBuiltin pins the native builtin set against the
// self-hosted compiler's own list of builtin names.
//
// A new builtin is four classifications — internal/checker, internal/interp,
// internal/caps and internal/platforms — plus the two self-host capability
// MIRRORS, and each of those six has a completeness test. None of them covers
// the self-hosted COMPILER, which is the expensive half: parser.fern's name
// list, ircore, ir (op + extension kind id), irlower, asmcore and the three
// emitters.
//
// That gap is not hypothetical. `sleep_ns` was classified in all six places
// and never lowered, so `coreutils/sleep.fern` compiled on the native leg and
// died on the self-host one with `error[E001]: undefined function "sleep_ns"`.
// #9060 merged with that suite red and main stayed broken until #9081. Five
// more builtins were in the same state behind it (#9085).
//
// This is the name half, and it is the half that fails SILENTLY: an unknown
// name is E001 at the call site, which reads like the program's mistake rather
// than the compiler's. The lowering half is self-reporting by comparison — a
// name that reaches the parser with no IR op behind it stops at a diagnostic
// naming the bail site, which is a plain bug report.
//
// There is deliberately NO exemption list. One would have been sized to five
// on the day it was written and to six the day sleep_ns landed, and a pinned
// "these are fine to be missing" table is the tracking-list workaround the
// engineering bar forbids. If a builtin genuinely cannot reach the self-host,
// that is a fact worth failing on until someone writes down why.
func TestSelfHostKnowsEveryNativeBuiltin(t *testing.T) {
	native := nativeBuiltinNames(t)
	selfHost := selfHostBuiltinNames(t)

	var missing []string
	for _, name := range native {
		if !selfHost[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Errorf("%d builtin(s) the native checker knows and the self-hosted compiler does not: %s\n"+
		"Each one compiles on the native leg and fails the self-host leg with E001 at the call site.\n"+
		"Teach examples/self_host/parser.fern's builtin_function_names(), and lower it: ircore,\n"+
		"ir (op + extension kind id), irlower, asmcore, and asm_ir / asm_arm64_ir / wasm_ir.\n"+
		"See #9085. Do not add an exemption here.",
		len(missing), strings.Join(missing, ", "))
}

// nativeBuiltinNames is every bare builtin the checker registers, taken from a
// real Check rather than by reading checker.go: the question is what the
// compiler actually offers, and a regex over the source answers a different
// one.
//
// Two exclusions, neither of them a builtin the self-host could name:
// `__`-prefixed entries are compiler internals with no surface spelling, and
// `__method_` entries are reached through a receiver rather than as a name.
func nativeBuiltinNames(t *testing.T) []string {
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
	var out []string
	for name := range info.FuncSigs {
		if declared[name] || strings.HasPrefix(name, "__") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no builtins found in a checked probe — this test would pass on anything")
	}
	return out
}

var selfHostBuiltinNamesRE = regexp.MustCompile(
	`(?s)function builtin_function_names\(\): string\[\] \{.*?return \[(.*?)\n    \];`)

// selfHostBuiltinNames reads builtin_function_names() out of the self-hosted
// parser. Reading the declaration rather than running the compiler is the
// point: the question is whether the two LISTS agree, which a behavioural test
// can only sample one name at a time.
func selfHostBuiltinNames(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", "parser.fern"))
	if err != nil {
		t.Fatalf("read self-host parser.fern: %v", err)
	}
	m := selfHostBuiltinNamesRE.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("cannot find builtin_function_names() in examples/self_host/parser.fern — " +
			"the pattern no longer matches, so this test proves nothing")
	}
	// Strip comments: the list is annotated, and a name inside a comment is
	// documentation, not a registration.
	body := regexp.MustCompile(`//[^\n]*`).ReplaceAllString(m[1], "")
	out := map[string]bool{}
	for _, q := range fernStringRE.FindAllStringSubmatch(body, -1) {
		out[q[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("builtin_function_names() parsed to an empty list — this test would fail on everything")
	}
	return out
}
