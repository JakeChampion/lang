package e2eselfhost

import "testing"

// jsonArrayIRCases exercise the element-polymorphic array serialiser
// `(xs: T[]) to_json[T: Json]()` (the std/json array `to_json`) through the
// self-hosted compiler's IR path. The self-host emits generic bodies by
// ERASURE — so the one emitted `(xs: T[]) to_json()` body bakes in the i32
// element dispatch and can't serialise a string/struct array. irlower
// special-cases the CALL SITE (`arr.to_json()`, where the element type IS
// known) into an inline loop whose per-element `arr[i].to_json()` dispatches to
// the right impl: i32 -> __fn_i32__to_json, string -> __fn_string__to_json, a
// derived struct -> its synthesised `<Struct>.to_json`. Issue #2766.
//
// Each case is a complete, IR-eligible program (inline `trait Json` + the
// primitive impls + the array method) — the same shape as the
// derive-default IR cases. Bundling the full std/json is deliberately avoided:
// std/json as a whole isn't IR-eligible, so it routes through the AST emitter
// where this fix does not apply (a legacy gap that, per project scope, does not
// need fixing). The program returns the rendered JSON's length as its exit
// code.
const jsonArrayPrelude = `trait Json { function to_json(self: Self): string; }
impl Json for i32 { function to_json(self: Self): string { return self.to_string(); } }
impl Json for string { function to_json(self: Self): string { return "\"" + self + "\""; } }
pub function (xs: T[]) to_json[T: Json](): string {
    var out: string = "[";
    var i: i32 = 0;
    while (i < xs.len()) { if (i > 0) { out = out + ","; } out = out + xs[i].to_json(); i = i + 1; }
    return out + "]";
}
`

var jsonArrayIRCases = []struct {
	name string
	prog string // full program tail (top-level decls + main) appended to the prelude
	exit int
}{
	// [1,2,3] -> "[1,2,3]" (7 chars). Scalar elements.
	{"i32-array", `function main(): i32 { var a: i32[] = [1, 2, 3]; return a.to_json().len(); }`, 7},
	// ["x","y"] -> `["x","y"]` (9 chars). String elements (each quoted).
	{"string-array", `function main(): i32 { var a: string[] = ["x", "y"]; return a.to_json().len(); }`, 9},
	// [] -> "[]" (2 chars). The empty array short-circuits the loop.
	{"empty-array", `function main(): i32 { var a: i32[] = []; return a.to_json().len(); }`, 2},
	// `@derive(Json)` struct elements: each renders as a JSON object via its
	// synthesised to_json. [{"id":1,"tag":"x"},{"id":2,"tag":"y"}] (39 chars).
	{"struct-array",
		`@derive(Json) struct Item { id: i32, tag: string }
function main(): i32 { var items: Item[] = [Item { id: 1, tag: "x" }, Item { id: 2, tag: "y" }]; return items.to_json().len(); }`, 39},
}

func jsonArraySrc(prog string) string {
	return "import \"std/i32\";\nimport \"std/string\";\n" + jsonArrayPrelude + "\n" + prog + "\n"
}

// TestSelfHostJsonArrayIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code.
func TestSelfHostJsonArrayIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
		for _, tc := range jsonArrayIRCases {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, jsonArraySrc(tc.prog), target); code != tc.exit {
					t.Errorf("exited %d, want %d\n%s", code, tc.exit, stderr)
				}
			})
		}
	}
}
