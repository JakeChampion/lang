package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Array-method helpers reached only via METHOD syntax survive tree-shaking on
// the self-host IR path. A call like `p.join("|")` / `xs.reversed()` records only
// the bare field ("join" / "reversed") as a reachable name, but the dispatch
// targets the `<mod>__method_Array_<field>` helper free function (modload mangles
// it with a module prefix). The tree-shaker kept functions by EXACT name, so the
// helper was pruned and the whole module fell to the AST emitter. ts_keep now
// ties a `*__method_Array_<m>` helper to its bare-field reference, so the helper
// is retained and the method lowers on the IR path. This flipped strings /
// string_slice_extract / array_combinators from AST to IR.
//
// These run through the asm_load_run driver (which tree-shakes + IR-lowers); each
// asserts `-decide == "ir"` and matches the interpreter oracle.
var arrayMethodTreeshakeIRCases = []struct {
	name string
	src  string
}{
	// string[].join via method syntax → __method_Array_join helper.
	{"join", `import "std/array";
function main(): i32 { var p: string[] = ["a", "bb", "ccc"]; var j: string = p.join("|"); return j.len(); }`},
	// i32[].reversed via method syntax → __method_Array_reversed helper.
	{"reversed", `import "std/array";
function main(): i32 { var a: i32[] = [5, 3, 8, 1]; var b: i32[] = a.reversed(); return b[0] * 10 + b[3]; }`},
	// split (string method) feeding join (array-method helper).
	{"split-join", `import "std/string";
import "std/array";
function main(): i32 { var p: string[] = "a,b,c,d".split(","); var j: string = p.join("-"); return j.len(); }`},
}

func TestSelfHostArrayMethodTreeshakeIR(t *testing.T) {
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

	for _, tc := range arrayMethodTreeshakeIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "amts_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			if !strings.Contains(asm, "__method_Array_") {
				t.Errorf("%s: no __method_Array_ helper in asm (pruned by tree-shaking?)", tc.name)
			}
			bin := buildBin(t, gcc, dir, "amts_"+tc.name+"_bin", asm)
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
