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
