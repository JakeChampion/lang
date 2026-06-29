package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mapStructKeyIRCases exercise STRUCT / ENUM Map keys through the self-hosted
// x86-64 IR path (#2671). The self-host map is a linear-scan parallel-array
// assoc list; a struct/enum key is stored as a pointer in keys[] and compared
// by VALUE through its `@derive(Eq)`-synthesised `K.eq` method, which irlower
// threads in as the symbol `__fn_<K>__eq` and the four `__fern_map_*` runtime
// routines dispatch via an indirect `call *` (in place of the i32 / string
// compare). A struct key carrying a STRING field — and an enum key with a
// STRING payload — prove the comparison is STRUCTURAL (a fresh, value-equal
// key built from a concatenation, living in a different buffer, must hit the
// same entry). Exit codes are the oracle (8-bit, so kept < 256).
//
// Scope: x86-64 self-host only. The wasm self-host backend uses a different
// (hash-map) runtime that still supports scalar/string keys only; struct keys
// there are a separate slice (it ignores irlower's eq-fn symbol, so this change
// leaves its behaviour byte-identical).
var mapStructKeyIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Struct key with i32 fields: insert / overwrite / has / get_or / len.
	// fresh value-equal key hits the same entry; field order matters.
	{"struct-i32",
		`import "core/cmp"; @derive(cmp.Eq) struct P { x: i32, y: i32 } function main(): i32 { var m: Map[P, i32] = map_new(8); m = m.insert(P { x: 1, y: 2 }, 10); m = m.insert(P { x: 3, y: 4 }, 20); if (m.get_or(P { x: 1, y: 2 }, 0 - 1) != 10) { return 101; } if (m.get_or(P { x: 3, y: 4 }, 0 - 1) != 20) { return 102; } if (m.get_or(P { x: 9, y: 9 }, 0 - 1) != 0 - 1) { return 103; } if (!m.has(P { x: 1, y: 2 })) { return 104; } if (m.has(P { x: 2, y: 1 })) { return 105; } m = m.insert(P { x: 1, y: 2 }, 99); if (m.len() != 2) { return 106; } return m.get_or(P { x: 1, y: 2 }, 0 - 1); }`,
		99},
	// Struct key with a STRING field — structural eq (the fresh "a"+"da" key is
	// a distinct buffer). Also covers without() (functional delete).
	{"struct-string-field",
		`import "core/cmp"; @derive(cmp.Eq) struct N { first: string, rank: i32 } function main(): i32 { var m: Map[N, i32] = map_new(8); m = m.insert(N { first: "ada", rank: 1 }, 10); m = m.insert(N { first: "bob", rank: 2 }, 20); if (m.get_or(N { first: "a" + "da", rank: 1 }, 0 - 1) != 10) { return 101; } if (m.has(N { first: "ada", rank: 9 })) { return 102; } var (m2, ok) = m.without(N { first: "ada", rank: 1 }); m = m2; if (!ok) { return 103; } if (m.has(N { first: "ada", rank: 1 })) { return 104; } if (m.len() != 1) { return 105; } return m.get_or(N { first: "bob", rank: 2 }, 0 - 1); }`,
		20},
	// Enum key with payload + unit + string-payload variants; sums values via
	// the for (k, v) MapIter loop. 10 + 20 + 30 = 60.
	{"enum-key-iter",
		`import "core/cmp"; @derive(cmp.Eq) enum Tag { A(i32), B, C(string) } function main(): i32 { var m: Map[Tag, i32] = map_new(8); m = m.insert(A(1), 10); m = m.insert(B, 20); m = m.insert(C("x" + "y"), 30); if (m.get_or(C("xy"), 0) != 30) { return 101; } if (m.get_or(A(2), 0 - 1) != 0 - 1) { return 102; } if (m.len() != 3) { return 103; } var t: i32 = 0; for (k, v) in m { t = t + v; } return t; }`,
		60},
}

// TestSelfHostMapStructKeyIRX86_64 compiles each case with the self-hosted
// x86-64 driver and asserts the exit code, proving struct/enum Map keys lower
// correctly through the self-host IR path (the derived-eq indirect dispatch in
// __fern_map_set/get/has/delete).
func TestSelfHostMapStructKeyIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range mapStructKeyIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
