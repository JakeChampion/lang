package modload_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/printer"
)

var determinismMatrix = map[string]map[string]string{
	"single_file": {
		"main.fern": `function main(): i32 { return 0; }`,
	},

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

	"stdlib_imports": {
		"main.fern": `
import "std/math";
import "std/array";
function main(): i32 { return 0; }
`,
	},
}

func TestLoadDeterministic(t *testing.T) {
	for name, files := range determinismMatrix {
		name, files := name, files
		t.Run(name, func(t *testing.T) {
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

func mustLoadPrint(t *testing.T, entryPath string) string {
	t.Helper()
	prog, _, err := modload.Load(entryPath)
	if err != nil {
		t.Fatalf("modload.Load: %v", err)
	}
	return printer.Print(prog)
}
