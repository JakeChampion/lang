package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func checkModuleSource(t *testing.T, src string) error {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = Check(prog)
	return err
}

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
    let m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    return 0;
}`,
			want: []string{"map key type Key", "`@derive(cmp.Eq, cmp.Hash)`", cmpImportFrag},
			fixes: []string{`import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }
function main(): i32 {
    let m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    return 0;
}`},
		},
		{
			name: "E045 enum map key",
			bad: `import "core/map";
enum K { A, B(i32) }
function main(): i32 {
    let m: Map[K, i32] = Map {};
    m = m.insert(B(1), 10);
    return 0;
}`,
			want: []string{"map key type K", "`@derive(cmp.Eq, cmp.Hash)`", cmpImportFrag},
			fixes: []string{`import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
enum K { A, B(i32) }
function main(): i32 {
    let m: Map[K, i32] = Map {};
    m = m.insert(B(1), 10);
    return 0;
}`},
		},
		{
			name: "E041 structural equality",
			bad: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`,
			want: []string{"does not implement `Eq`", "`@derive(cmp.Eq)`", "`impl cmp.Eq for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Eq)
struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Eq for P {
    function eq(self: Self, other: Self): boolean { return self.x == other.x; }
}
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`},
		},
		{
			name: "E041 structural ordering",
			bad: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`,
			want: []string{"does not implement `Ord`", "`@derive(cmp.Ord)`", "`impl cmp.Ord for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Ord)
struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Ord for P {
    function cmp(self: Self, other: Self): i32 { return self.x - other.x; }
}
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 2 };
    if (a < b) { return 1; }
    return 0;
}`},
		},
		{
			name: "E038 print needs Display",
			bad: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    print(a);
    return 0;
}`,
			want: []string{"does not implement `Display`", "`to_string(): string` method", "`@derive(cmp.Display)`", "`impl cmp.Display for P`", cmpImportFrag},
			fixes: []string{`import "core/cmp";
@derive(cmp.Display)
struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    print(a);
    return 0;
}`, `import "core/cmp";
struct P { x: i32 }
impl cmp.Display for P {
    function to_string(self: Self): string { return "P"; }
}
function main(): i32 {
    let a: P = P { x: 1 };
    print(a);
    return 0;
}`, `struct P { x: i32 }
function (p: P) to_string(): string { return "P"; }
function main(): i32 {
    let a: P = P { x: 1 };
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

func TestMapKeyFallbackHintNamesQualifiedDerive(t *testing.T) {
	err := checkModuleSource(t, `import "core/map";
function main(): i32 {
    let m: Map[f64, i32] = Map { 1.5: 10 };
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
    let m: Map[Key, i32] = Map { Key { a: 1 }: 10 };
    return 0;
}`); err != nil {
		t.Fatalf("the suggested key spelling must check clean: %v", err)
	}
}

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

	if err := checkSource(t, `function main(): i32 {
    let a: boolean = true;
    let b: i32 = 1;
    let c: u32 = 2 as u32;
    let d: f64 = 1.5;
    let e: string = "hello";
    let g: u8 = 3;
    let h: str = slice_unchecked(e, 1, 3);
    return h.len();
}`); err != nil {
		t.Fatalf("str / u8 and every suggested spelling must check clean: %v", err)
	}
}

var foreignHintCases = []struct {
	name   string
	bad    string
	orphan string
	local  string
}{
	{
		name: "E041 equality",
		bad: foreignProg(`    if (a == b) { return 1; }
    return 0;`),
		orphan: `import "std/regex";
import "std/result";
import "core/cmp";
impl cmp.Eq for regex.Match {
    function eq(self: Self, other: Self): boolean { return true; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "E041 ordering",
		bad: foreignProg(`    if (a < b) { return 1; }
    return 0;`),
		orphan: `import "std/regex";
import "std/result";
import "core/cmp";
impl cmp.Ord for regex.Match {
    function cmp(self: Self, other: Self): i32 { return 0; }
}
function main(): i32 { return 0; }`,
	},
	{
		name: "E038 print",
		bad: foreignProg(`    print(a);
    return 0;`),
		orphan: `import "std/regex";
import "std/result";
import "core/cmp";
impl cmp.Display for regex.Match {
    function to_string(self: Self): string { return "m"; }
}
function main(): i32 { return 0; }`,
		local: `import "std/regex";
import "std/result";
pub function (m: regex.Match) to_string(): string { return "m"; }
function main(): i32 {
    let a: regex.Match = regex.Match { start: 0, end: 0 };
    print(a);
    return 0;
}`,
	},
	{
		name: "E045 map key",
		bad: `import "std/regex";
import "std/result";
import "core/map";
function main(): i32 {
    let m: Map[regex.Match, i32] = Map {};
    m = m.insert(regex.Match { start: 0, end: 0 }, 1);
    return 0;
}`,
	},
}

func foreignProg(body string) string {
	return `import "std/regex";
import "std/result";
function main(): i32 {
    let a: regex.Match = regex.Match { start: 0, end: 0 };
    let b: regex.Match = regex.Match { start: 0, end: 0 };
` + body + `
}`
}

func TestDeriveHintsForForeignTypesNameAWritableFix(t *testing.T) {
	for _, tc := range foreignHintCases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModuleSource(t, tc.bad)
			if err == nil {
				t.Fatalf("expected a diagnostic for:\n%s", tc.bad)
			}
			msg := err.Error()
			if strings.Contains(msg, "regex__Match") {
				t.Errorf("hint leaks the mangled name, which is not a spelling anyone can write: %v", msg)
			}
			for _, w := range []string{"regex.Match", "in module `regex`, which declares it"} {
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

func TestMapHintNamesAnImportThatWorks(t *testing.T) {
	err := checkModuleSource(t, `function main(): i32 {
    let m: Map[string, i32] = Map {};
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
    let m: Map[string, i32] = Map {};
    return 0;
}`); err != nil {
		t.Fatalf("the import the hint names must make the program check: %v", err)
	}
}

func TestDemangleNamedRewritesOnlyDeclaredNames(t *testing.T) {
	mangled := map[string]bool{"regex__RMatch": true, "box__Box": true, "point__Point": true}
	for in, want := range map[string]string{
		"P":                          "P",
		"regex__RMatch":              "regex.RMatch",
		"(regex__RMatch, Foo__Bar)":  "(regex.RMatch, Foo__Bar)",
		"box__Box[point__Point]":     "box.Box[point.Point]",
		"regex__RMatch[]":            "regex.RMatch[]",
		"Map[regex__RMatchX, i32]":   "Map[regex__RMatchX, i32]",
		`variable "regex__RMatch_x"`: `variable "regex__RMatch_x"`,
		"Map[string, __method_P_eq]": "Map[string, __method_P_eq]",
	} {
		if got := demangleNamed(in, mangled); got != want {
			t.Errorf("demangleNamed(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveDefaultNeedsAFieldDefault(t *testing.T) {
	err := checkModuleSource(t, `import "core/cmp";
struct Inner { v: i32 }
@derive(cmp.Default)
struct Outer { i: Inner, n: i32 }
function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected E021 for a Default derive over a field with no default")
	}
	if code := firstErrCode(err); code != "E021" {
		t.Errorf("code = %s, want E021: %v", code, err)
	}
	if !strings.Contains(err.Error(), "field i of type Inner does not implement cmp.Default") || !strings.Contains(err.Error(), "4:1") {
		t.Errorf("error = %v", err)
	}
	if err := checkModuleSource(t, `import "core/cmp";
@derive(cmp.Default)
struct Inner { v: i32 }
@derive(cmp.Default)
struct Outer { i: Inner, n: i32 }
function main(): i32 { let o: Outer = Outer.default(); return o.n + o.i.v; }`); err != nil {
		t.Errorf("a nested derive should check clean: %v", err)
	}
}
