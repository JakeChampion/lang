package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapStructKeyWasmIR is the wasm gate for struct/enum Map keys
// (#2671) — the wasm sibling of TestSelfHostMapStructKeyIRX86_64. The wasm
// self-host map is a HASH map (not the x86 linear-scan assoc list), so a
// struct/enum key needs BOTH a derived hash (to bucket) AND a derived eq (to
// resolve collisions). irlower emits op_map_new(kind=2) carrying the key's
// `K.hash|K.eq` symbols; wasm_ir threads their funcref-table slots into the
// box via $__fern_map_new_struct, and $__fern_map_hk / $__fern_map_keq dispatch
// them through `call_indirect (type $fn1)` / `(type $fn2)`.
//
// Because the stdin-only driver doesn't load core/cmp, the derived `K.hash`
// inlines its field hashes (primitive fields fold value-wise, string fields
// fold per byte) rather than calling `impl Hash for i32`/`string` — otherwise
// the unknown `.hash()` call would bail the whole module to the AST backend,
// which has no struct-key support. Asserts the oracle exit code AND that the
// IR struct-key constructor was emitted ($__fern_map_new_struct in the WAT).
// Exit codes <= 125 (wasi rejects >= 126).
func TestSelfHostMapStructKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host struct-key map wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name     string
		src      string
		expected int
	}{
		// Struct key with i32 fields: insert / overwrite / has / get_or / len.
		// A fresh value-equal key hits the same entry; field order matters.
		{"struct-i32",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function main(): i32 { var m: Map[P, i32] = map_new(8); m = m.insert(P { x: 1, y: 2 }, 10); m = m.insert(P { x: 3, y: 4 }, 20); if (m.get_or(P { x: 1, y: 2 }, 0 - 1) != 10) { return 101; } if (m.get_or(P { x: 3, y: 4 }, 0 - 1) != 20) { return 102; } if (m.get_or(P { x: 9, y: 9 }, 0 - 1) != 0 - 1) { return 103; } if (!m.has(P { x: 1, y: 2 })) { return 104; } if (m.has(P { x: 2, y: 1 })) { return 105; } m = m.insert(P { x: 1, y: 2 }, 99); if (m.len() != 2) { return 106; } return m.get_or(P { x: 1, y: 2 }, 0 - 1); }`,
			99},
		// Struct key with a STRING field — structural hash + eq (the fresh
		// "a"+"da" key is a distinct buffer). Also covers without() (delete).
		{"struct-string-field",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct N { first: string, rank: i32 } function main(): i32 { var m: Map[N, i32] = map_new(8); m = m.insert(N { first: "ada", rank: 1 }, 10); m = m.insert(N { first: "bob", rank: 2 }, 20); if (m.get_or(N { first: "a" + "da", rank: 1 }, 0 - 1) != 10) { return 101; } if (m.has(N { first: "ada", rank: 9 })) { return 102; } var (m2, ok) = m.without(N { first: "ada", rank: 1 }); m = m2; if (!ok) { return 103; } if (m.has(N { first: "ada", rank: 1 })) { return 104; } if (m.len() != 1) { return 105; } return m.get_or(N { first: "bob", rank: 2 }, 0 - 1); }`,
			20},
		// Enum key with payload + unit + string-payload variants. A fresh
		// "x"+"y" key must hit the same C("xy") entry (structural).
		{"enum-key",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { A(i32), B, C(string) } function main(): i32 { var m: Map[Tag, i32] = map_new(8); m = m.insert(A(1), 10); m = m.insert(B, 20); m = m.insert(C("x" + "y"), 30); if (m.get_or(C("xy"), 0) != 30) { return 101; } if (m.get_or(A(2), 0 - 1) != 0 - 1) { return 102; } if (m.len() != 3) { return 103; } return m.get_or(B, 0) + m.get_or(A(1), 0); }`,
			30},
		// Grow / rehash stress: 20 keys into a hint-4 map force several
		// rehashes; all must read back, then an overwrite + delete.
		{"struct-grow",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function main(): i32 { var m: Map[P, i32] = map_new(4); var i: i32 = 0; while (i < 20) { m = m.insert(P { x: i, y: i * 2 }, i + 1); i = i + 1; } if (m.len() != 20) { return 101; } var j: i32 = 0; while (j < 20) { if (m.get_or(P { x: j, y: j * 2 }, 0 - 1) != j + 1) { return 1 + j; } j = j + 1; } var (m2, ok) = m.without(P { x: 7, y: 14 }); m = m2; if (!ok) { return 102; } if (m.has(P { x: 7, y: 14 })) { return 103; } if (m.len() != 19) { return 104; } return 42; }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			if !strings.Contains(string(wat), "$__fern_map_new_struct") {
				t.Errorf("%q did not reach the struct-key IR path (no $__fern_map_new_struct in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "ir_prog_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("struct-key map wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
