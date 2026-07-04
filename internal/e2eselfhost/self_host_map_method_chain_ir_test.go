package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Chained ops on a map-returning BUILTIN method CALL result on the self-host IR
// path. `m.insert(k, v).len()` / `.get_or(..)` / `.has(..)` — the receiver of the
// outer op is itself a map-returning builtin call (insert/set/delete all return
// Map[K, V]), often nested (`m.insert(..).insert(..)`). #4016 gave the map-method
// dispatch an ExprCall arm, but it only knows the GENERIC verbs (merge/extend/...
// via the map_ret_fns registry) and can't resolve a nested-call receiver, so the
// builtin chain fell through with mtype "" and the chained `.len()` mis-dispatched
// to op_arr_len — reading the map box's keys[] pointer slot as an array length, a
// silent miscompile (`m.insert(1,10).len()` returned a garbage 96, not 1). The
// arm now falls back to expr_map_type_tag (which recurses through insert/set/
// delete) so the chained op dispatches as a map op. Each case is oracle-checked
// against the interpreter.
var mapMethodChainIRCases = []struct {
	name string
	src  string
}{
	// insert-chain then len: the result is a fresh map of length 1.
	{"insert-len", `import "core/map";
function main(): i32 { var m: Map[i32, i32] = map_new(4); return m.insert(1, 10).len(); }`},
	// double insert-chain then len → 2.
	{"insert-insert-len", `import "core/map";
function main(): i32 { var m: Map[i32, i32] = map_new(4); return m.insert(1, 10).insert(2, 20).len(); }`},
	// insert-chain then get_or reads the just-inserted value.
	{"insert-get_or", `import "core/map";
function main(): i32 { var m: Map[i32, i32] = map_new(4); return m.insert(7, 70).get_or(7, 0); }`},
	// string keys: insert-chain then get_or on a string-keyed map.
	{"insert-get_or-strkey", `import "core/map";
function main(): i32 { var m: Map[string, i32] = map_new(4); return m.insert("a", 5).get_or("a", 0); }`},
}

func TestSelfHostMapMethodChainIR(t *testing.T) {
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

	for _, tc := range mapMethodChainIRCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "mapchain_"+tc.name+".fern")
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
			bin := buildBin(t, gcc, dir, "mapchain_"+tc.name+"_bin", asm)
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
