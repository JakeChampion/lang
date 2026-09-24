package e2eselfhost

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

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// TestSelfHostBuiltinArgDifferentialX86_64 pins that a free builtin's
// ARGUMENTS are checked, for every builtin the self-host checker types.
//
// They were not. checker.fern typed only each builtin's RESULT, and typing the
// result is what makes a call to one infer — so an inferred call was accepted
// whatever it was handed. `f32_bits(y)` on an f64 compiled and reinterpreted
// the low 32 bits of the double's bit pattern, where native refuses it with
// E038: the self-host checker accepting what native rejects, which
// docs/NATIVE-CONVERGENCE.md calls the dangerous direction (#9987).
//
// The corpus is DERIVED, not listed. The names come from the self-host's own
// parameter tables and the signatures from native's live FuncSigs, so a builtin
// added to either side is covered here without anyone editing this file — the
// same reason TestSelfHostTypesEveryIntrinsicFamily reads both tables rather
// than an allowlist.
//
// Each program calls one builtin with every argument deliberately mistyped, in
// statement position so no destination type of the test's own invention can
// colour the answer. The Go checker is the sole oracle, and what is compared is
// the whole DIAGNOSTIC — "argument 2: expected i32, got string" — not the code
// set.
//
// The codes alone are not enough, and the difference is the accept-what-native-
// rejects direction this exists to close. Type `__memchr`'s byte parameter
// `string` by mistake and the self-host still rejects arguments 1 and 3 while
// native rejects all three: both sides report E038, the sets match, and the one
// argument the self-host now accepts is hidden behind its neighbours. Comparing
// the messages pins WHICH argument each side flags and WHAT it expected there,
// so a wrong parameter type in the table is a red gate in either direction.
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
			// The oracle has to be saying something, or the row proves only
			// that two compilers agree about a program neither objects to.
			if len(want) == 0 {
				t.Errorf("%s: the Go checker reports nothing for a call whose every argument is "+
					"mistyped — the generated call is wrong, not the compilers\nsrc: %s", tc.name, tc.src)
			}
		})
	}
}

// builtinMistypedCalls generates one deliberately ill-typed call per free
// builtin the self-host types.
func builtinMistypedCalls(t *testing.T) []struct{ name, src string } {
	t.Helper()
	sigs := nativeBuiltinSigs(t)
	var out []struct{ name, src string }
	for _, name := range selfHostParameterisedBuiltins(t) {
		sig, ok := sigs[name]
		if !ok {
			t.Errorf("examples/self_host/checker.fern gives %q parameter types and native declares no such "+
				"builtin — one of the two tables names something the other has never heard of", name)
			continue
		}
		// A type-parameter argument accepts anything, so there is nothing to
		// mistype.
		if hasTypeParamParam(sig) {
			continue
		}
		args := make([]string, 0, len(sig.Params))
		for _, p := range sig.Params {
			args = append(args, mistypedFor(p))
		}
		// A zero-parameter builtin has no argument to mistype; handing it one
		// is the arity half of the same silence, which native answers E004.
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

// mistypedFor is a literal of a type the parameter cannot accept: a string
// where anything else is wanted, and an integer where a string is. Both
// spellings are ones no coercion rescues, which is what makes the expected
// diagnostic E038 rather than a settled literal.
func mistypedFor(p ast.Type) string {
	if _, isStr := p.(ast.StringType); isStr {
		return "1"
	}
	return `"x"`
}

// nativeBuiltinSigs is the live FuncSigs table, minus the method entries —
// read from a real Check for the reason the intrinsic-family gate gives: the
// question is what the compiler offers, and a regex over checker.go answers a
// different one.
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

// selfHostParameterisedBuiltins reads the names out of checker.fern's two
// parameter tables: intrinsic_params, expanding a family it matches with a
// predicate into the names that predicate lists, and builtin_sigs' free rows.
func selfHostParameterisedBuiltins(t *testing.T) []string {
	t.Helper()
	body := selfHostFileSection(t, "checker.fern",
		`(?s)function intrinsic_params\(.*?\n// type_debug renders a Type`)
	// The raw floor is self-host only: native declares none of it, so it has
	// no oracle here. TestSelfHostRawFloorIsTypedWhole gates it against irlower.
	floor := selfHostFileSection(t, "checker.fern",
		`(?s)pub struct RawFloorSig \{.*?\nfunction raw_floor_find\(.*?\n\}\n`)
	body = strings.Replace(body, floor, "", 1)
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

// The patterns selfHostParameterisedBuiltins reads its sections with: a
// quoted builtin name, a family predicate applied to the name under test, and
// the name a builtin_sigs row opens with.
var (
	quotedNameRE      = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`)
	familyPredicateRE = regexp.MustCompile(`(is_[a-z0-9_]+)\(name\)`)
	sigRowNameRE      = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)\(`)
)

// selfHostFileSection returns the span of a self-host source the pattern
// matches, failing loudly rather than silently reading empty when the source
// has been reshaped — a gate that reads nothing passes on anything.
func selfHostFileSection(t *testing.T, file, pattern string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "examples", "self_host", file))
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

// diagLines renders diagnostics as `CODE: MESSAGE`, sorted, for comparison
// between the two checkers. Sorted because neither side promises an order, and
// the question here is which argument each one flagged, not when.
func diagLines(ds []driverDiag) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.code+": "+d.msg)
	}
	sort.Strings(out)
	return out
}
