package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/parser"
)

// TestSelfHostTypesEveryIntrinsicFamily pins the `__`-prefixed runtime
// intrinsics the native checker types against the two self-host tables that
// have to agree with it: checker.fern's intrinsic_result, which gives the
// expression written around the call a type, and semsource.fern's
// intrinsic_contracts, which gives the semantic boundary the call's ownership.
//
// TestSelfHostKnowsEveryNativeBuiltin is the NAME half and skips `__` entries
// outright — they have no surface spelling, so a program cannot name one and
// E001 cannot fire. That is what made this half invisible: an intrinsic the
// self-host parser knows and lowers, but whose RESULT the self-host checker
// leaves unknown, compiles fine and silently collapses every expression built
// around the call. The boundary then refuses the whole function, and the
// reason it reports names the binding rather than the builtin underneath it.
// 3,057 of the corpus census's refusals were that shape.
//
// The gate is by FAMILY, not by an allowlist: each family below is defined by
// the predicate native registers it under, so a name added to one of them on
// the native side fails here until the self-host carries it too. Families the
// self-host has not reached yet are absent entirely rather than exempted —
// there is no "these are fine to be missing" table, and adding one would be
// the tracking-list workaround the engineering bar forbids. What is here is
// what is claimed; what is claimed is checked whole.
//
// The `__c_callN` trampolines have no family here because the self-hosted IR
// does not lower them on any backend — a program naming one stops at the bail
// site rather than reaching a contract. `__alloc_reuse` is in the same state.
// Both are lowering gaps behind the name, which is the half the name gate
// above describes as self-reporting; giving them contracts would only move
// the failure to a link error.
func TestSelfHostTypesEveryIntrinsicFamily(t *testing.T) {
	native := nativeIntrinsicSigs(t)
	lowerable := selfHostLoweredIntrinsics(t)
	checkerTyped := selfHostTypedIntrinsics(t)
	contracted := selfHostIntrinsicContracts(t)

	for _, family := range intrinsicFamilies {
		names := family.members(native)
		// An intrinsic the self-host IR does not lower at all cannot be
		// contracted: a contract would send it to the backends as a direct
		// call to a symbol no runtime defines. That is a LOWERING gap behind
		// the name — the half TestSelfHostKnowsEveryNativeBuiltin describes as
		// self-reporting — so it is not this gate's to fail on. The filter is
		// computed from the self-host sources, not listed here.
		names = withLowering(names, lowerable)
		if len(names) == 0 {
			t.Errorf("family %q matched no native intrinsic the self-host lowers — either the "+
				"predicate has drifted from what internal/checker registers, or the lowering "+
				"went away, so this family is no longer gating anything", family.name)
			continue
		}
		var untyped, uncontracted []string
		for _, n := range names {
			if !checkerTyped[n] {
				untyped = append(untyped, n)
			}
			if !contracted[n] {
				uncontracted = append(uncontracted, n)
			}
		}
		sort.Strings(untyped)
		sort.Strings(uncontracted)
		if len(untyped) > 0 {
			t.Errorf("family %q: %d intrinsic(s) native types and examples/self_host/checker.fern's "+
				"intrinsic_result does not: %s\nA call to one of these types as unknown, which "+
				"collapses the literal, array or operator holding it.",
				family.name, len(untyped), strings.Join(untyped, ", "))
		}
		if len(uncontracted) > 0 {
			t.Errorf("family %q: %d intrinsic(s) with no contract in examples/self_host/semsource.fern's "+
				"intrinsic_contracts: %s\nThe semantic boundary refuses every function that calls one.",
				family.name, len(uncontracted), strings.Join(uncontracted, ", "))
		}
	}
}

// A family is the set of names native registers under one shape. The predicate
// is what makes this a completeness test rather than a list: it is evaluated
// against the live FuncSigs table, so a sibling added there joins the family
// without anyone editing this file.
type intrinsicFamily struct {
	name    string
	members func(map[string]*sigShape) []string
}

// sigShape is the part of a native signature this gate compares: how many
// parameters the call takes and what the result spells. The self-host tables
// are keyed on name and arity, so that is the pairing worth pinning.
type sigShape struct {
	params int
	result string
}

var intrinsicFamilies = []intrinsicFamily{
	{"f64 primitive", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, s *sigShape) bool {
			return strings.HasSuffix(n, "_f64") && !strings.HasPrefix(n, "__c_call") &&
				s.params == 1 && s.result == "ast.FloatType"
		})
	}},
	{"bit count", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, s *sigShape) bool {
			base := strings.HasPrefix(n, "__clz") || strings.HasPrefix(n, "__ctz") ||
				strings.HasPrefix(n, "__popcount")
			return base && s.params == 1
		})
	}},
	{"raw memory", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			for _, p := range []string{"__alloc", "__free", "__load_", "__store_", "__memcpy", "__memset", "__heap_", "__ptr_width"} {
				if strings.HasPrefix(n, p) {
					return true
				}
			}
			return false
		})
	}},
	{"byte scan", func(m map[string]*sigShape) []string {
		return matching(m, func(n string, _ *sigShape) bool {
			for _, s := range []string{"__sum_bytes", "__ascii_run", "__count_byte", "__memchr", "__rmemchr", "__mismatch"} {
				if n == s {
					return true
				}
			}
			return false
		})
	}},
}

func withLowering(names []string, lowerable map[string]bool) []string {
	var out []string
	for _, n := range names {
		if lowerable[n] {
			out = append(out, n)
		}
	}
	return out
}

// selfHostLoweredIntrinsics is every intrinsic name the self-hosted lowering
// mentions — irlower for the AST path, ir.fern for the op table. A name in
// neither has no IR behind it on this compiler at all.
func selfHostLoweredIntrinsics(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range []string{"irlower.fern", "ir.fern", "ssarc.fern"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}

func matching(m map[string]*sigShape, pred func(string, *sigShape) bool) []string {
	var out []string
	for n, s := range m {
		if pred(n, s) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// nativeIntrinsicSigs reads the `__` entries out of a real Check rather than
// out of checker.go's text, for the reason the name-half gate gives: the
// question is what the compiler offers, and a regex over the source answers a
// different one.
func nativeIntrinsicSigs(t *testing.T) map[string]*sigShape {
	t.Helper()
	prog, err := parser.Parse(`function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check probe: %v", err)
	}
	out := map[string]*sigShape{}
	for name, sig := range info.FuncSigs {
		if !strings.HasPrefix(name, "__") || strings.HasPrefix(name, "__method_") {
			continue
		}
		out[name] = &sigShape{params: len(sig.Params), result: fmt.Sprintf("%T", sig.Result)}
	}
	if len(out) == 0 {
		t.Fatal("no `__` intrinsics in a checked probe — this test would pass on anything")
	}
	return out
}

// Every intrinsic the section names, whether compared against one at a time
// or listed for a loop to walk.
var selfHostTypedRE = regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`)

// selfHostTypedIntrinsics reads every intrinsic name intrinsic_result and
// its helpers mention. Reading the declaration rather than running the
// compiler is deliberate, as in the name-half gate: the question is whether
// the two tables agree, which a behavioural test can only sample one name at
// a time.
func selfHostTypedIntrinsics(t *testing.T) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "checker.fern",
		regexp.MustCompile(`(?s)// The ten f64 primitives.*?\n// The builtin results a call carries`))
	out := map[string]bool{}
	for _, m := range selfHostTypedRE.FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	// The C trampolines are matched by prefix rather than named one at a time.
	if strings.Contains(body, `"__c_call"`) {
		for n := 0; n <= 4; n++ {
			for _, suffix := range []string{"", "_f32", "_f64"} {
				out["__c_call"+string(rune('0'+n))+suffix] = true
			}
		}
	}
	return out
}

// selfHostIntrinsicContracts reads intrinsic_contracts and everything it
// calls. The loop-built families (the f64 primitives, the bit counts and the
// trampolines) are spelled as string lists rather than one contract each, so
// the lists are read too.
func selfHostIntrinsicContracts(t *testing.T) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "semsource.fern",
		regexp.MustCompile(`(?s)function intrinsic_contracts\(.*?\n// The builtins whose result is one of`))
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`ssasem\.Contract \{ name: "(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(body, -1) {
		out[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`\[((?:"__[A-Za-z0-9_]+",?\s*)+)\]`).FindAllStringSubmatch(body, -1) {
		for _, n := range regexp.MustCompile(`"(__[A-Za-z0-9_]+)"`).FindAllStringSubmatch(m[1], -1) {
			out[n[1]] = true
		}
	}
	if strings.Contains(body, `"__c_call" + util.i32_to_string(n)`) {
		for n := 0; n <= 4; n++ {
			for _, suffix := range []string{"", "_f32", "_f64"} {
				out["__c_call"+string(rune('0'+n))+suffix] = true
			}
		}
	}
	return out
}

func selfHostSection(t *testing.T, file string, re *regexp.Regexp) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", file))
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	body := re.FindString(string(b))
	if body == "" {
		t.Fatalf("%s: the section this gate reads has been renamed or reshaped; "+
			"point the pattern at its new form rather than deleting the gate", file)
	}
	return body
}

// TestSelfHostParameterisesEveryTypedBuiltin pins the invariant #9987 broke:
// a free builtin whose RESULT the self-host checker types must also carry its
// PARAMETER types.
//
// The two halves are not independent. Typing the result is what makes a call
// to one infer at all, and an inferred call with no parameters to check
// against is accepted whatever it is handed — `f32_bits(y)` on an f64
// compiled and reinterpreted the low 32 bits of the double, where native
// refuses it with E038. A builtin the self-host types neither way is not this
// gate's business: it reports the #4451 "unregistered builtin" bail instead,
// which is a different gap and a loud one.
//
// So the direction checked is one-way. Result without parameters fails;
// parameters without a result cannot occur, because free_builtin_sig reads the
// result table for its own return type. Surface builtins are builtin_sigs'
// rows, which carry both halves in one spelling.
func TestSelfHostParameterisesEveryTypedBuiltin(t *testing.T) {
	typed := selfHostTypedBuiltins(t, `(?s)// The ten f64 primitives.*?\n// The builtin results a call carries`, false)
	parameterised := selfHostTypedBuiltins(t, `(?s)function intrinsic_params\(.*?\n// type_debug renders a Type`, true)
	if len(typed) == 0 || len(parameterised) == 0 {
		t.Fatal("one of the two builtin tables read empty — this test would pass on anything")
	}
	var unchecked []string
	for n := range typed {
		if !parameterised[n] {
			unchecked = append(unchecked, n)
		}
	}
	sort.Strings(unchecked)
	if len(unchecked) > 0 {
		t.Errorf("%d builtin(s) whose result examples/self_host/checker.fern types and whose parameters it "+
			"does not: %s\nA call to one of these infers, so nothing refuses a wrong argument and the "+
			"self-host accepts what native rejects.", len(unchecked), strings.Join(unchecked, ", "))
	}
}

// selfHostTypedBuiltins reads every builtin name one table's section spells,
// without selfHostTypedIntrinsics' `__` prefix filter.
//
// `members` reads only the rows that CLAIM the name, which the parameter table
// marks with the `true` half of its answer. Without it a row reading
// `return ([], false)` — the table's way of saying "not a free builtin" —
// would count as covering the name it tests, so the gate would pass on exactly
// the shape it exists to catch. The result table has no such half, so it is
// read whole.
//
// A family the table matches with a predicate rather than one name at a time
// (`is_f64_primitive(name)`) contributes the names that predicate lists, so
// covering ten builtins in one line reads as covering ten builtins. Without
// this the reader of the table and the reader of this gate would disagree
// about what it says.
func selfHostTypedBuiltins(t *testing.T, section string, members bool) map[string]bool {
	t.Helper()
	body := selfHostSection(t, "checker.fern", regexp.MustCompile(section))
	if members {
		var claiming []string
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, ", true)") {
				claiming = append(claiming, line)
			}
		}
		body = strings.Join(claiming, "\n")
	}
	out := map[string]bool{}
	harvest := func(src string) {
		for _, m := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllStringSubmatch(src, -1) {
			out[m[1]] = true
		}
	}
	harvest(body)
	for _, m := range regexp.MustCompile(`(is_[a-z0-9_]+)\(name\)`).FindAllStringSubmatch(body, -1) {
		harvest(selfHostSection(t, "checker.fern",
			regexp.MustCompile(`(?s)function `+m[1]+`\(name: string\): boolean \{.*?\n\}`)))
	}
	return out
}

// TestSelfHostRawFloorIsTypedWhole pins the raw-memory floor the runtime
// helpers are written on. Native registers none of it, so the family gate
// above cannot see it; the source of truth is what irlower lowers. Every such
// name must have a row in checker.fern's raw_floor_sigs (which
// semsource.fern's raw_floor_contracts reads, so a typed name is contracted)
// and in ssarc.fern's raw_floor_ops, or the typed path refuses the helper that
// calls it.
func TestSelfHostRawFloorIsTypedWhole(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	lowered := map[string]bool{}
	for _, m := range regexp.MustCompile(`cid\.name == "(__raw_[a-z0-9_]+|__syscall[0-9])"`).FindAllStringSubmatch(read("irlower.fern"), -1) {
		lowered[m[1]] = true
	}
	if len(lowered) == 0 {
		t.Fatal("irlower.fern lowers no raw-floor name — the pattern has drifted from its spelling")
	}
	typed := rawFloorTableNames(t, read("checker.fern"), `function raw_floor_sigs\(\): RawFloorSig\[\] \{`)
	arms := rawFloorTableNames(t, read("ssarc.fern"), `function raw_floor_ops\(\): RawOps\[\] \{`)
	var untyped, unlowered []string
	for n := range lowered {
		if !typed[n] {
			untyped = append(untyped, n)
		}
		if !arms[n] {
			unlowered = append(unlowered, n)
		}
	}
	sort.Strings(untyped)
	sort.Strings(unlowered)
	if len(untyped) > 0 {
		t.Errorf("irlower lowers %s, which checker.fern's raw_floor_sigs does not type", strings.Join(untyped, ", "))
	}
	if len(unlowered) > 0 {
		t.Errorf("irlower lowers %s, which ssarc.fern's raw_floor_ops has no row for", strings.Join(unlowered, ", "))
	}
}

// rawFloorTableNames is the `name: "..."` rows of the table function whose header
// matches `header`, up to its closing brace.
func rawFloorTableNames(t *testing.T, src, header string) map[string]bool {
	t.Helper()
	body := regexp.MustCompile(`(?s)` + header + `(.*?)\n\}`).FindStringSubmatch(src)
	if body == nil {
		t.Fatalf("no table matching %s", header)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`name: "(__[a-z0-9_]+)"`).FindAllStringSubmatch(body[1], -1) {
		out[m[1]] = true
	}
	return out
}
