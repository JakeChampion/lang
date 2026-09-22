package e2eselfhost

import (
	"bytes"
	"os/exec"
	"testing"
)

// TestSelfHostMapKeyAnnotationDifferentialX86_64 pins that both checkers
// answer a WRITTEN `Map[K, V]` the same way, in every position one can be
// written.
//
// Neither did. Each validated a map key only where a value SETTLED into a map
// destination — and only for a struct or enum key at that — so the annotated
// spelling of a program the literal form rejects walked straight past both
// (#10009). That is the spelling that reached codegen: a float key took the
// self-host's string column and segfaulted (#9973), and a tuple key
// type-checked and then answered the default instead of the value inserted
// under it.
//
// What is compared is the whole DIAGNOSTIC, not the code set. The two sides
// agreeing that a program draws E045 says nothing about whether they agree on
// WHICH key they refused or what they would accept instead, and a checker
// that accepts what native rejects is the direction
// docs/NATIVE-CONVERGENCE.md calls dangerous.
func TestSelfHostMapKeyAnnotationDifferentialX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	for _, tc := range mapKeyAnnotationRows {
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
				t.Errorf("%s: the two checkers disagree about this map key.\nnative:    %s\nself-host: %s\nsrc:\n%s",
					tc.name, joinOrNone(want), joinOrNone(got), tc.src)
			}
			if tc.rejected && len(want) == 0 {
				t.Errorf("%s: the Go checker reports nothing for a key it is supposed to refuse — "+
					"the row is wrong, not the compilers\nsrc:\n%s", tc.name, tc.src)
			}
			if !tc.rejected && len(want) != 0 {
				t.Errorf("%s: the Go checker refuses a key this row expects it to accept: %s\nsrc:\n%s",
					tc.name, joinOrNone(want), tc.src)
			}
		})
	}
}

// mapKeyAnnotationRows covers each position a `Map[K, V]` annotation can
// appear in, with a key both checkers must refuse and — in the last four
// rows — keys both must accept, so a rule that over-reaches is as red as one
// that under-reaches.
var mapKeyAnnotationRows = []struct {
	name     string
	src      string
	rejected bool
}{
	{"var-annotation", `import "core/map";
function main(): i32 { var m: Map[f64, i32] = map_new(2); return 0; }
`, true},
	{"parameter", `import "core/map";
function take(m: Map[f64, i32]): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"return-type", `import "core/map";
function make(): Map[f64, i32] { return map_new(2); }
function main(): i32 { return 0; }
`, true},
	{"struct-field", `import "core/map";
struct S { m: Map[f64, i32] }
function main(): i32 { return 0; }
`, true},
	{"nested-in-an-array", `import "core/map";
function take(ms: Map[f64, i32][]): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"nested-in-a-tuple", `import "core/map";
function take(p: (string, Map[f64, i32])): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"empty-literal-with-an-annotation", `import "core/map";
function main(): i32 { var m: Map[f64, i32] = Map {}; return 0; }
`, true},
	{"non-empty-literal-with-an-annotation", `import "core/map";
function main(): i32 { var m: Map[f64, i32] = Map { 1.5: 7 }; return 0; }
`, true},
	{"f32-key", `import "core/map";
function take(m: Map[f32, i32]): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"tuple-key", `import "core/map";
function take(m: Map[(i32, i32), i32]): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"struct-key-without-the-derives", `import "core/map";
struct K { a: i32 }
function take(m: Map[K, i32]): i32 { return 0; }
function main(): i32 { return 0; }
`, true},
	{"struct-key-with-the-derives", `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32 }
function take(m: Map[K, i32]): i32 { return m.len(); }
function main(): i32 { var m: Map[K, i32] = map_new(2); return take(m); }
`, false},
	{"narrow-integer-keys", `import "core/map";
function take(a: Map[u8, i32], b: Map[u32, i32]): i32 { return 0; }
function main(): i32 { var m: Map[u8, i32] = map_new(2); return 0; }
`, false},
	{"string-key", `import "core/map";
function take(m: Map[string, i32]): i32 { return m.len(); }
function main(): i32 { var m: Map[string, i32] = map_new(2); return take(m); }
`, false},
	{"generic-key-parameter", `import "core/map";
function take[T](m: Map[T, i32]): i32 { return 0; }
function main(): i32 { return 0; }
`, false},
	{"lambda-parameter", `import "core/map";
function main(): i32 {
    var f = (m: Map[f64, i32]): i32 => { return 0; };
    return 0;
}
`, true},
	{"lambda-return-type", `import "core/map";
function main(): i32 {
    var f = (n: i32): Map[f64, i32] => { return map_new(2); };
    return 0;
}
`, true},
}
