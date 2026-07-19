package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Return-type-directed instantiation of a generic CONSTRUCTOR. A no-argument
// generic function whose type parameter appears only in its return type
// (`mk[T](): Box[T]`) is bound from the call-site annotation (`var b: Box[i32]
// = mk()`) — the arguments alone can't. The monomorphiser (parser.fern) now:
//   1. promotes such a return-only unbounded type var into type_params (it is
//      bindable from the annotation, not just from a param),
//   2. binds it via infer_inst_ret in the annotated-var position (mono_var_init),
//   3. retypes the constructor's `return Box { xs: [] }` generic struct literal
//      — whose empty-array field yields no element type to infer from — using
//      the function's concrete return type (ms_stmt StmtReturn).
// Without all three the template stays un-monomorphised, its `Box { xs: [] }`
// can't lower, and the whole module drops to the legacy AST emitter and
// SEGFAULTS. std/set's `set_new` → `set_of` is the real-world victim. Each case
// is oracle-checked against the interpreter and routing-pinned to "ir".
var genericCtorIRCases = []struct {
	name string
	src  string
}{
	// The minimal failing shape: a no-arg constructor with an empty-array field,
	// instantiated from the var annotation. len 0 + 40 = 40.
	{"empty_ctor_i32", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[i32] = mk(); return b.xs.len() + 40; }`},
	// A string element type: the clone must carry the concrete `string[]` field.
	{"empty_ctor_string", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[string] = mk(); return b.xs.len() + 41; }`},
	// Nested: a generic function calling ANOTHER no-arg constructor with its own
	// (already-concrete) type param supplied by the local annotation — the exact
	// `set_of` → `set_new` shape. len 0 + 42 = 42.
	{"nested_ctor", `struct Bag[T] { xs: T[] }
function empty[T](): Bag[T] { return Bag { xs: [] }; }
function make[T](x: T): Bag[T] { var b: Bag[T] = empty(); return b; }
function main(): i32 { var b: Bag[i32] = make(3); return b.xs.len() + 42; }`},
	// The constructed value is actually used: append then read back → 1*100+7.
	{"ctor_then_use", `struct Box[T] { xs: T[] }
function mk[T](): Box[T] { return Box { xs: [] }; }
function main(): i32 { var b: Box[i32] = mk(); var ys = b.xs.append(7); return ys.len() * 100 + ys[0]; }`},
	// The real-world case: std/set's set_of builds through set_new (a no-arg
	// generic constructor) — distinct count of {1,2,3} = 3.
	{"std_set_of", `import "std/set";
function main(): i32 { var s = set.set_of([1, 2, 2, 3, 3, 3]); return s.len(); }`},
}

func TestSelfHostGenericCtorIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "gci")
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

	for _, tc := range genericCtorIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "gci_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			// Oracle: the native interpreter's exit code.
			_, want := runFixtureInterp(t, entry, "")
			// The whole point of the fix is that these route IR, not AST.
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "gci_"+tc.name+"_bin", asm)
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
