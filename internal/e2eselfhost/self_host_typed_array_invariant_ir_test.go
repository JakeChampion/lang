package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostTypedArrayInvariantIR pins one invariant across every expression
// form that produces a TYPED array: if the lowering can name the element kind,
// it must also treat the value as an array.
//
// The two halves of that question are answered by separate, independently
// hand-maintained lists in irlower. `is_arr` (in lower_stmt_var) enumerates
// callee names — `__alloc_u8`, `str_split`, `args`, `is_arr_ret_fn`, the string
// methods — and `expr_is_strarr` / `expr_is_f64arr` / `expr_is_i64arr` each
// enumerate their own. Nothing tied them together, so they could disagree, and
// they did: `args()` was in `expr_is_strarr` from the day the builtin landed and
// missing from `is_arr` ever since. The visible symptom was oddly narrow —
// `a[i]` and `a.len()` lowered fine, `for s in a` bailed the whole function —
// which is why it survived until the AST fallback was deleted and the bail
// turned into a hard error (#5983).
//
// So each case below does BOTH: an index/len read and a `for`-loop, over the
// same expression. A list that gains an entry on one side and not the other
// fails here rather than years later.
//
// Half the fixtures are self-host dialect with no interpreter oracle
// (`str_split` is E001 natively; `.bytes()` / `.chars()` / `.lines()` need
// `std/string` imported), so their exit codes are stated and were verified
// against the emitted wasm. The three that ARE native-valid carry the same value
// on both, which is what makes the stated ones credible.
func TestSelfHostTypedArrayInvariantIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, rerr := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		if werr := os.WriteFile(filepath.Join(dir, name), src, 0o644); werr != nil {
			t.Fatalf("write %s: %v", name, werr)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	for _, tc := range []struct {
		name string
		src  string
		exit int
		argv []string
	}{
		// args() — THE case. Both halves, so the original asymmetry (index ok,
		// for-loop bails) cannot come back silently.
		{"args-index", `function main(): i32 { var a = args(); return a.len(); }`, 3, []string{"A", "B"}},
		{"args-for", `function main(): i32 { var a = args(); var n = 0; for s in a { n = n + 1; } return n; }`, 3, []string{"A", "B"}},

		// string[] from the split builtin and from a declared return type.
		{"split-index", `function main(): i32 { var xs = str_split("a-b-c", "-"); return xs.len(); }`, 3, nil},
		{"split-for", `function main(): i32 { var xs = str_split("a-b-c", "-"); var n = 0; for s in xs { n = n + s.len(); } return n; }`, 3, nil},
		{"strret-index", "function mk(): string[] { return [\"ab\", \"cde\"]; }\nfunction main(): i32 { var xs = mk(); return xs[0].len() + xs[1].len(); }", 5, nil},
		{"strret-for", "function mk(): string[] { return [\"ab\", \"cde\"]; }\nfunction main(): i32 { var xs = mk(); var n = 0; for s in xs { n = n + s.len(); } return n; }", 5, nil},

		// The string methods that yield arrays.
		{"bytes-for", `function main(): i32 { var b = "abc".bytes(); var n = 0; for c in b { n = n + 1; } return n; }`, 3, nil},
		{"chars-for", `function main(): i32 { var c = "abcd".chars(); var n = 0; for x in c { n = n + 1; } return n; }`, 4, nil},
		{"lines-for", "function main(): i32 { var l = \"a\\nb\\nc\".lines(); var n = 0; for x in l { n = n + 1; } return n; }", 3, nil},

		// The WIDE element kinds, whose flags are the other two predicates.
		{"i64ret-for", "function mk(): i64[] { return [5000000000, 1]; }\nfunction main(): i32 { var xs = mk(); var n = 0; for v in xs { n = n + 1; } return n; }", 2, nil},
		{"f64ret-for", "function mk(): f64[] { return [1.5, 2.5]; }\nfunction main(): i32 { var xs = mk(); var n = 0; for v in xs { n = n + 1; } return n; }", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — an element-kind predicate and the array-ness decision disagree about this expression again", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, append([]string{"run", watPath}, tc.argv...)...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
