package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/modload"
)

// checkModuleSource type-checks src with its imports resolved, so a test
// can compile the exact spelling a diagnostic suggests. checkSource
// parses in isolation and cannot see `import "core/cmp";`.
func checkModuleSource(t *testing.T, src string) error {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Check(prog)
	return err
}

// TestDeriveHintsSpellWhatCompiles pairs every diagnostic that tells the
// reader to derive or implement a core/cmp trait with a program that
// follows it verbatim (#6990). `bad` must report a message containing
// each `want` fragment, and each `fix` — which spells exactly what those
// fragments suggest — must check clean.
//
// The pairing is the point. There is no prelude, so a hint naming a bare
// `@derive(Eq)` sends the reader into E021 "unknown trait"; only the
// qualified `cmp.Eq` plus `import "core/cmp";` resolves. A message-text
// assertion on its own cannot tell those two apart.
func TestDeriveHintsSpellWhatCompiles(t *testing.T) {
	const cmpImportFrag = "`import \"core/cmp\";`"

	cases := []struct {
		name  string
		bad   string
		want  []string
		fixes []string
	}{
		{
			name: "E045 struct map key",
			bad: `import "core/map";
struct Key { a: i32, b: string }
function main(): i32 {
    var m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    return 0;
}`,
			want: []string{"map key type Key", "`@derive(cmp.Eq, cmp.Hash)`", cmpImportFrag},
			fixes: []string{`import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }
function main(): i32 {
    var m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    return 0;
}`},
		},
		{
			name: "E045 enum map key",
			bad: `import "core/map";
enum K { A, B(i32) }
function main(): i32 {
    var m: Map[K, i32] = Map {};
    m = m.insert(B(1), 10);
    return 0;
}`,
			want: []string{"map key type K", "`@derive(cmp.Eq, cmp.Hash)`", cmpImportFrag},
			fixes: []string{`import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
enum K { A, B(i32) }
function main(): i32 {
    var m: Map[K, i32] = Map {};
    m = m.insert(B(1), 10);
    return 0;
}`},
		},
		{
			name: "E041 structural equality",
			bad: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`,
			want: []string{"does not implement `Eq`", "`@derive(cmp.Eq)`", "`impl cmp.Eq for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Eq for P {
    function eq(self: Self, other: Self): boolean { return self.x == other.x; }
}
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`},
		},
		{
			name: "E041 structural ordering",
			bad: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`,
			want: []string{"does not implement `Ord`", "`@derive(cmp.Ord)`", "`impl cmp.Ord for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Ord)
struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Ord for P {
    function cmp(self: Self, other: Self): i32 { return self.x - other.x; }
}
function main(): i32 {
    var a: P = P { x: 1 };
    var b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`},
		},
		{
			name: "E038 print needs Display",
			bad: `struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    print(a);
    return 0;
}`,
			want: []string{"does not implement `Display`", "`to_string(): string` method", "`@derive(cmp.Display)`", "`impl cmp.Display for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Display)
struct P { x: i32 }
function main(): i32 {
    var a: P = P { x: 1 };
    print(a);
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Display for P {
    function to_string(self: Self): string { return "P"; }
}
function main(): i32 {
    var a: P = P { x: 1 };
    print(a);
    return 0;
}`, `struct P { x: i32 }
function (p: P) to_string(): string { return "P"; }
function main(): i32 {
    var a: P = P { x: 1 };
    print(a);
    return 0;
}`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.bad)
			if err == nil {
				t.Fatalf("expected a diagnostic for:\n%s", tc.bad)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("hint is missing %q, got: %v", w, err)
				}
			}
			for i, fix := range tc.fixes {
				if err := checkModuleSource(t, fix); err != nil {
					t.Errorf("fix %d does not check clean — the hint suggests a spelling the checker rejects: %v\nsrc:\n%s", i, err, fix)
				}
			}
		})
	}
}

// The E045 catch-all — a map-literal key that is neither an integer,
// a string, nor a struct/enum — names the same qualified spelling as
// the struct and enum branches, and that spelling is a usable key.
func TestMapKeyFallbackHintNamesQualifiedDerive(t *testing.T) {
	err := checkModuleSource(t, `import "core/map";
function main(): i32 {
    var m: Map[f64, i32] = Map { 1.5: 10 };
    return 0;
}`)
	if err == nil {
		t.Fatal("expected E045 for an f64 map key")
	}
	for _, w := range []string{"`@derive(cmp.Eq, cmp.Hash)`", "`import \"core/cmp\";`"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("hint is missing %q, got: %v", w, err)
		}
	}
	if err := checkModuleSource(t, `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32 }
function main(): i32 {
    var m: Map[Key, i32] = Map { Key { a: 1 }: 10 };
    return 0;
}`); err != nil {
		t.Fatalf("the suggested key spelling must check clean: %v", err)
	}
}

// TestUnknownTypeHintOnlyFiresForUnknownNames pins the E064 respelling
// table against the failure it shipped with (#6990): `str` (the borrowed
// string view) and `u8` are real, type-checking types, so entries
// offering to replace them with `string` / `u32` were both dead and
// wrong. Every surviving entry names a type that does not exist, and
// every replacement it suggests does.
func TestUnknownTypeHintOnlyFiresForUnknownNames(t *testing.T) {
	for name, want := range map[string]string{
		"bool":   "did you mean `boolean`?",
		"int":    "did you mean `i32`?",
		"long":   "did you mean `i32`?",
		"uint":   "did you mean `u32`?",
		"double": "did you mean `f64`?",
		"String": "did you mean `string`?",
	} {
		err := checkSource(t, "function f(x: "+name+"): i32 { return 0; }\nfunction main(): i32 { return 0; }")
		if err == nil {
			t.Errorf("%s: expected E064, got none", name)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got: %v", name, want, err)
		}
	}

	// The real types the table must never offer to replace, alongside
	// every spelling it does suggest.
	if err := checkSource(t, `function main(): i32 {
    var a: boolean = true;
    var b: i32 = 1;
    var c: u32 = 2 as u32;
    var d: f64 = 1.5;
    var e: string = "hello";
    var g: u8 = 3;
    var h: str = e[1:3];
    return h.len();
}`); err != nil {
		t.Fatalf("str / u8 and every suggested spelling must check clean: %v", err)
	}
}

// foreignHintCases are the derive/impl hints reached with a type from
// ANOTHER module. `regex.RMatch` is a `pub struct` in std/regex with no
// Eq / Ord / Display, so every one of these fires on a name modload has
// mangled to `regex__RMatch`.
var foreignHintCases = []struct {
	name string
	bad  string
	// orphan is the local `impl` the hint must NOT tell the reader to
	// write; the checker refuses it, which is why the hint sends them to
	// the declaring module instead. "" where the hint offers no impl.
	orphan string
	// local is a fix that DOES work from here, "" where none exists.
	local string
}{
	{
		name: "E041 equality",
		bad: foreignProg(`    if (a == b) { return 1; }
    return 0;`),
		orphan: `import "std/regex";
import "core/cmp";
impl cmp.Eq for regex.RMatch {
    function eq(self: Self, other: Self): boolean { return true; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "E041 ordering",
		bad: foreignProg(`    if (a < b) { return 1; }
    return 0;`),
		orphan: `import "std/regex";
import "core/cmp";
impl cmp.Ord for regex.RMatch {
    function cmp(self: Self, other: Self): i32 { return 0; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "E038 print",
		bad: foreignProg(`    print(a);
    return 0;`),
		orphan: `import "std/regex";
import "core/cmp";
impl cmp.Display for regex.RMatch {
    function to_string(self: Self): string { return "m"; }
}
function main(): i32 { return 0; }`,
		// The receiver-method route the message offers first. Only a
		// TRAIT impl is orphan-checked, so this one is writable here.
		local: `import "std/regex";
pub function (m: regex.RMatch) to_string(): string { return "m"; }
function main(): i32 {
    var a: regex.RMatch = regex.RMatch { found: true, start: 0, end: 0 };
    print(a);
    return 0;
}`,
	},
	{
		name: "E045 map key",
		bad: `import "std/regex";
import "core/map";
function main(): i32 {
    var m: Map[regex.RMatch, i32] = Map {};
    m = m.insert(regex.RMatch { found: true, start: 0, end: 0 }, 1);
    return 0;
}`,
	},
}

// foreignProg wraps `body` in a program that binds two std/regex values.
func foreignProg(body string) string {
	return `import "std/regex";
function main(): i32 {
    var a: regex.RMatch = regex.RMatch { found: true, start: 0, end: 0 };
    var b: regex.RMatch = regex.RMatch { found: true, start: 0, end: 0 };
` + body + `
}`
}

// TestDeriveHintsForForeignTypesNameAWritableFix is the cross-module half
// of the pairing above, and it is the half #7000 did not have: every one
// of its cases declares the type locally, so the mangled spelling never
// appeared. Reached with a type from another module, the same four hints
// were wrong twice over — they printed the internal `regex__RMatch`, and
// both routes they named are ones this checker refuses (`@derive`
// annotates a declaration you do not own, a local impl of a foreign trait
// for a foreign type is an orphan impl).
//
// So the contract has two directions: every route a hint OFFERS must
// check clean, and every route it WITHHOLDS must be one the checker
// really rejects. Asserting only the first lets a hint go quiet about a
// fix that works; asserting only the second lets it recommend one that
// does not.
func TestDeriveHintsForForeignTypesNameAWritableFix(t *testing.T) {
	for _, tc := range foreignHintCases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.bad)
			if err == nil {
				t.Fatalf("expected a diagnostic for:\n%s", tc.bad)
			}
			msg := err.Error()
			if strings.Contains(msg, "regex__RMatch") {
				t.Errorf("hint leaks the mangled name, which is not a spelling anyone can write: %v", msg)
			}
			for _, w := range []string{"regex.RMatch", "in module `regex`, which declares it"} {
				if !strings.Contains(msg, w) {
					t.Errorf("hint is missing %q, got: %v", w, msg)
				}
			}
			if tc.orphan != "" {
				if !strings.Contains(msg, "orphan impl") {
					t.Errorf("hint offers an impl without saying it has to go in `regex`: %v", msg)
				}
				if err := checkModuleSource(t, tc.orphan); err == nil {
					t.Error("a local impl for a foreign trait and a foreign type checked clean — the hint's reason for sending the reader to the declaring module no longer holds")
				} else if !strings.Contains(err.Error(), "orphan impl") {
					t.Errorf("expected an orphan-impl diagnostic, got: %v", err)
				}
			} else if strings.Contains(msg, "orphan impl") {
				t.Errorf("hint mentions an orphan impl but names no impl route: %v", msg)
			}
			if tc.local != "" {
				if err := checkModuleSource(t, tc.local); err != nil {
					t.Errorf("the route the hint offers does not check clean: %v\nsrc:\n%s", err, tc.local)
				}
			}
		})
	}
}

// The E001 Map hint names an import, so it gets the same pairing: the
// program it describes has to check clean.
func TestMapHintNamesAnImportThatWorks(t *testing.T) {
	err := checkModuleSource(t, `function main(): i32 {
    var m: Map[string, i32] = Map {};
    return 0;
}`)
	if err == nil {
		t.Fatal("expected E001 for Map without its import")
	}
	if want := "`import \"core/map\";`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("hint is missing %q, got: %v", want, err)
	}
	if err := checkModuleSource(t, `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = Map {};
    return 0;
}`); err != nil {
		t.Fatalf("the import the hint names must make the program check: %v", err)
	}
}

// demangleAll is what keeps a hint's type label writable. The composite
// case is the reason it is not a single strings.Replace: `demangle` undoes
// one mangling, and a tuple carries one per element.
func TestDemangleAllRewritesEveryPart(t *testing.T) {
	for in, want := range map[string]string{
		"P":                          "P",
		"regex__RMatch":              "regex.RMatch",
		"(a__A, b__B)":               "(a.A, b.B)",
		"box__Box[point__Point]":     "box.Box[point.Point]",
		"regex__RMatch[]":            "regex.RMatch[]",
		"Map[k__K, i32]":             "Map[k.K, i32]",
		"__method_P_eq":              "__method_P_eq",
		"Map[string, __method_P_eq]": "Map[string, __method_P_eq]",
	} {
		if got := demangleAll(in); got != want {
			t.Errorf("demangleAll(%q) = %q, want %q", in, got, want)
		}
	}
}
