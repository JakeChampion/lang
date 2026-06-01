package modload_test

// Determinism guard for module loading.
//
// modload.Load is the entry point that every other compiler stage
// reads from: parsing, checker, IR builder, every backend. It walks
// the import graph, parses each module, runs the rewrite pass, and
// returns a single combined *ast.Program. If the order of functions
// / structs / enums in that Program depends on Go map iteration
// order, two loads of the same project would produce different
// Programs — and the downstream stages would then deterministically
// emit different output for the SAME source, breaking the byte-
// identical self-host fixed-point gates and reproducible builds.
//
// modload.loadAllPaths threads through a `loaded map[string]*…`
// table indexed by canonical path; the result combines those module
// programs into one. Iterating a Go map has randomized order, so
// "iteration that decides output order" is exactly where
// nondeterminism could leak. printer.Print serialises the whole
// Program (functions in p.Funcs order, structs in p.Structs order,
// every body); comparing it across repeated loads is a faithful
// witness.
//
// This sits one layer earlier than the IR determinism guard
// (internal/ir/determinism_test.go): if loading is deterministic
// here, the IR / codegen / interpreter downstream guards are
// testing only their own steps, not noise inherited from modload.

import (
	"testing"

	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/printer"
)

// determinismMatrix favours the modload paths most prone to ordering
// nondeterminism: multiple files (loaded-map iteration), transitive
// imports (worklist ordering), modules that share names (rename
// disambiguation), and many top-level decls (function/struct/enum
// merge order).
var determinismMatrix = map[string]map[string]string{
	// trivial: single-file project; baseline that the test
	// scaffolding itself is deterministic.
	"single_file": {
		"main.fern": `function main(): i32 { return 0; }`,
	},

	// fan-out: entry imports 3 siblings. The 3 module bodies merge
	// into the combined Program in whatever order the loaded map
	// iterates — Go randomises this per process.
	"fan_out_three_imports": {
		"main.fern": `
import "./a";
import "./b";
import "./c";
function main(): i32 { return a_top() + b_top() + c_top(); }
`,
		"a.fern": `pub function a_top(): i32 { return 1; }`,
		"b.fern": `pub function b_top(): i32 { return 2; }`,
		"c.fern": `pub function c_top(): i32 { return 3; }`,
	},

	// transitive: entry → a → b. Worklist must keep b after a, but
	// the loaded map can still iterate them in either order. If the
	// printed function list (entry / a / b funcs) ever depends on map
	// order, this case catches it.
	"transitive_chain": {
		"main.fern": `
import "./a";
function main(): i32 { return a_calls_b() + 10; }
`,
		"a.fern": `
import "./b";
pub function a_calls_b(): i32 { return b_leaf() * 2; }
`,
		"b.fern": `pub function b_leaf(): i32 { return 5; }`,
	},

	// struct_and_enum_decls: a project that contributes named
	// struct + enum types from each module. The combined Program
	// has 3 structs and 3 enums; their merge order is map-iteration
	// sensitive.
	"struct_and_enum_decls": {
		"main.fern": `
import "./a";
import "./b";
struct Main { mv: i32 }
enum MainE { ME1, ME2 }
function main(): i32 { return 0; }
`,
		"a.fern": `
pub struct A { av: i32 }
pub enum AE { AE1, AE2 }
pub function a_marker(): i32 { return 1; }
`,
		"b.fern": `
pub struct B { bv: i32 }
pub enum BE { BE1, BE2 }
pub function b_marker(): i32 { return 2; }
`,
	},
}

// TestLoadDeterministic loads each project several times and asserts
// every load produces a printer-byte-identical Program. A failure
// means modload's combined Program order depends on Go map
// iteration — which would propagate to every downstream stage and
// break the byte-identical self-host gates and reproducible builds.
func TestLoadDeterministic(t *testing.T) {
	for name, files := range determinismMatrix {
		name, files := name, files
		t.Run(name, func(t *testing.T) {
			// Same on-disk project across runs; only modload runs
			// each iteration. Different temp dirs across runs are
			// fine — modload's output should depend on file
			// contents + import structure, not absolute paths.
			dir := writeFiles(t, files)
			first := mustLoadPrint(t, dir+"/main.fern")
			for i := 0; i < 4; i++ {
				again := mustLoadPrint(t, dir+"/main.fern")
				if again != first {
					t.Fatalf("modload not deterministic on run %d: output differs (%d bytes vs %d bytes)",
						i+2, len(first), len(again))
				}
			}
		})
	}
}

// mustLoadPrint runs modload.Load on entryPath and returns the
// combined Program rendered through printer.Print. Failing the test
// on any error. printer.Print serialises function / struct / enum
// declarations in slice order + every body, so two loads of the
// same project must produce identical output for the load step to
// be deterministic.
func mustLoadPrint(t *testing.T, entryPath string) string {
	t.Helper()
	prog, _, err := modload.Load(entryPath)
	if err != nil {
		t.Fatalf("modload.Load: %v", err)
	}
	return printer.Print(prog)
}
