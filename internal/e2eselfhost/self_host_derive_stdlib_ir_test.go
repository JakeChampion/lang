package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The full `@derive` family on the self-host IR path THROUGH THE REAL stdlib.
// With stdlib loading + the treeshake pass (now applied by default whenever a
// stdlib root is given), a `@derive`d type's methods dispatch to core/cmp's
// real `eq`/`cmp`/`hash` and std/json's `to_json` — including the cases the
// inline lowerings (#3759 Eq, #3765 Ord) deliberately did NOT cover:
//   - `@derive(Hash)`     — core/cmp's __hash_mix_i32 + per-width / FNV string,
//   - `@derive(Json)`     — std/json's field-/variant-wise renderer,
//   - string-keyed `@derive(Ord)` — core/cmp's string `cmp` via sort.string_cmp.
//
// All route IR (the merged module fits the budget after treeshake) and match
// the native interpreter. Drives the self-hosted x86-64 loader (asm_load_run)
// with the repo's real stdlib as the root.
var deriveStdlibCases = []struct {
	name string
	src  string
}{
	{"hash-struct", `import "core/cmp";
@derive(cmp.Hash)
struct P { x: i32, y: i32 }
function main(): i32 { var a = P { x: 3, y: 5 }; var b = P { x: 3, y: 5 }; var c = P { x: 3, y: 6 }; if (a.hash() == b.hash() && a.hash() != c.hash()) { return 42; } return 0; }`},
	{"hash-string-field", `import "core/cmp";
@derive(cmp.Hash)
struct S { name: string, n: i32 }
function main(): i32 { var a = S { name: "hi", n: 1 }; var b = S { name: "hi", n: 1 }; if (a.hash() == b.hash()) { return 7; } return 0; }`},
	{"json-struct", `import "std/json";
@derive(json.Json)
struct P { x: i32, name: string }
function main(): i32 { var a = P { x: 7, name: "hi" }; return a.to_json().len(); }`},
	// `@derive(json.Json)` also synthesises the deserialise companion
	// `from_json(s): Result[Self, string]` (#2695 / #4416) — the self-host
	// twin of native's synthFromJson. Each case exercises a distinct way the
	// associated call `User.from_json(...)` is consumed, all of which must
	// route IR (the associated-call Option/Result return-type recovery in
	// irlower's four scrutinee/binding sites) and match the interpreter.
	// A direct `match (User.from_json(...))` over valid / missing-field /
	// invalid-JSON inputs → 9 + 7 + 5 = 21.
	{"json-from-struct", `import "std/json";
@derive(json.Json) struct User { id: i32, name: string }
function main(): i32 {
    var sum: i32 = 0;
    match (User.from_json("{\"id\":9,\"name\":\"x\"}")) { Ok(u) => { sum = sum + u.id; }, Err(e) => { sum = sum + 100; } }
    match (User.from_json("{\"id\":1}")) { Ok(u2) => { sum = sum + 100; }, Err(e) => { sum = sum + 7; } }
    match (User.from_json("nope")) { Ok(u3) => { sum = sum + 100; }, Err(e) => { sum = sum + 5; } }
    return sum;
}`},
	// A boolean field extracts through json_get_bool on the from_json (parse)
	// side → 14.
	{"json-from-bool", `import "std/json";
@derive(json.Json) struct Flag { id: i32, on: boolean }
function main(): i32 {
    match (Flag.from_json("{\"id\":4,\"on\":true}")) { Ok(f) => { if (f.on) { return f.id + 10; } return f.id; }, Err(e) => { return 1; } }
}`},
	// A full round-trip over boolean fields: to_json now renders a bool as
	// `true`/`false` (its Json impl) rather than the i32 `1`/`0` fallback, so
	// the serialised text parses straight back. Both a true and a false field
	// → 5 + 10 (on) + 100 (!off) = 115. Fails without the boolean-dispatch fix
	// (to_json would emit `"on":1`, which json_get_bool then rejects → Err).
	{"json-roundtrip-bool", `import "std/json";
@derive(json.Json) struct Flag { id: i32, on: boolean, off: boolean }
function main(): i32 {
    var f = Flag { id: 5, on: true, off: false };
    match (Flag.from_json(f.to_json())) { Ok(g) => { var r = g.id; if (g.on) { r = r + 10; } if (!g.off) { r = r + 100; } return r; }, Err(e) => { return 1; } }
}`},
	// The `var r = User.from_json(...)` binding form AND `?`-propagation of an
	// associated-call Result through a helper → 8 + 3 = 11.
	{"json-from-var-try", `import "std/json";
@derive(json.Json) struct User { id: i32, name: string }
function pick(s: string): Result[i32, string] { var u: User = User.from_json(s)?; return Ok(u.id); }
function main(): i32 {
    var r = User.from_json("{\"id\":8,\"name\":\"y\"}");
    var a = 0;
    match (r) { Ok(u) => { a = u.id; }, Err(e) => { a = 100; } }
    match (pick("{\"id\":3,\"name\":\"z\"}")) { Ok(n) => { a = a + n; }, Err(e) => { a = a + 100; } }
    return a;
}`},
	// A full serialise→deserialise round-trip over i32 / string fields → 7.
	{"json-roundtrip", `import "std/json";
@derive(json.Json) struct P { x: i32, name: string }
function main(): i32 {
    var p = P { x: 7, name: "hi" };
    match (P.from_json(p.to_json())) { Ok(q) => { return q.x; }, Err(e) => { return 1; } }
}`},
	{"ord-string-key", `import "core/cmp";
@derive(cmp.Ord)
struct S { name: string, id: i32 }
function main(): i32 { var a = S { name: "abc", id: 1 }; var b = S { name: "abd", id: 1 }; if (a.cmp(b) < 0 && b.cmp(a) > 0 && a.cmp(a) == 0) { return 42; } return 0; }`},
	{"combined-all", `import "core/cmp";
import "std/json";
@derive(cmp.Eq, cmp.Ord, cmp.Hash, json.Json)
struct Rec { name: string, id: i32 }
function main(): i32 { var a = Rec { name: "x", id: 1 }; var b = Rec { name: "y", id: 2 }; var n = 0; if (a.eq(a)) { n = n + 1; } if (a.cmp(b) < 0) { n = n + 1; } if (a.hash() != b.hash()) { n = n + 1; } if (a.to_json().len() > 0) { n = n + 1; } return n; }`},
}

func TestSelfHostDeriveStdlibIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range deriveStdlibCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "ds_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			// Loading the stdlib auto-applies treeshake, so the merged module
			// must route IR (decide observes the same all_eligible verdict).
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			// Emit + assemble + run; must match the oracle.
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "ds_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (native oracle)", tc.name, code, want)
			}
		})
	}
}
