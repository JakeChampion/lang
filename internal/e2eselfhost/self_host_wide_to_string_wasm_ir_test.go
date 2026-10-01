package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `.to_string()` on an i64 / u64 receiver on the wasm IR path — the wasm half
// of #5826.
//
// irlower lowers the wide receivers to call_direct __fern_{i64,u64}_to_string,
// which the register backends serve by compiling asmcore.rt_src_* Fern source
// through the IR pipeline. wasm has no such mechanism (its runtime helpers are
// hand-written WAT), so wasm_ir_deferrals_ok bailed the WHOLE module to the AST
// emitter rather than emit a call to an undefined function. It now serves them
// with $__fern_i64_to_str / $__fern_u64_to_str (i64_to_string_helper /
// u64_to_string_helper), so the deferral is gone.
//
// The formatters are gated one need each (@uses_{i32,i64,u64}_to_string), so a
// module formatting one width carries only that body — pinned below, because
// the obvious alternative (widen the existing @uses_i32_to_string gate to cover
// all three) would quietly bloat every i32 program with two unused formatters.

// wideToStringProg formats the interesting magnitudes of each width in one
// module: an ordinary i64, INT64_MIN (whose negation overflows back to itself,
// so the magnitude must run through i64.div_u), zero (the early-return branch
// with its own allocation), and a u64 with the high bit set (which the signed
// formatter would render as -1).
const wideToStringProg = `function main(): i32 {
    var a: i64 = 1234567890123 as i64;
    var b: i64 = (0 as i64) - (9223372036854775807 as i64) - (1 as i64);
    var c: u64 = 18446744073709551615 as u64;
    var z: i64 = 0 as i64;
    var s: string = a.to_string() + "|" + b.to_string() + "|" + c.to_string() + "|" + z.to_string();
    write(s + "\n");
    return 0;
}
`

// wideImports declares the formatters the programs call.
const wideImports = "import \"std/i32\";\nimport \"std/i64\";\nimport \"std/u64\";\n"

// TestSelfHostWideToStringWasmIR checks that the wide formatters render the
// same digits the native interpreter does, and that the AST lowering's
// hand-written formatters are gated one need each.
func TestSelfHostWideToStringWasmIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	l := newWasmStdlibLoader(t)

	emit := func(t *testing.T, src string) string {
		t.Helper()
		return string(l.emit(t, wideImports+src))
	}

	runWAT := func(t *testing.T, name, wat string) string {
		t.Helper()
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		watFile := filepath.Join(dir, "wide_"+name+".wat")
		if err := os.WriteFile(watFile, []byte(wat), 0o644); err != nil {
			t.Fatalf("write wat: %v", err)
		}
		out, err := exec.Command("wasmtime", "run", watFile).Output()
		if err != nil {
			t.Fatalf("wasmtime run %s: %v", name, err)
		}
		return string(out)
	}

	t.Run("digits", func(t *testing.T) {
		const want = "1234567890123|-9223372036854775808|18446744073709551615|0\n"
		if got := runWAT(t, "digits", emit(t, wideToStringProg)); got != want {
			t.Errorf("wide to_string = %q, want %q", got, want)
		}
	})

	// An f-string interpolant desugars to .to_string(), so it reaches the same
	// helper by a different syntactic route.
	t.Run("fstring", func(t *testing.T) {
		src := `function main(): i32 {
    var n: i64 = 42000000000 as i64;
    write(f"n={n}\n");
    return 0;
}
`
		if got := runWAT(t, "fstring", emit(t, src)); got != "n=42000000000\n" {
			t.Errorf("f-string i64 = %q, want %q", got, "n=42000000000\n")
		}
	})

	// One formatter per need: an i64-only module must not carry the i32 body,
	// nor an i32-only module the i64 one.
	for _, tc := range []struct {
		name    string
		src     string
		want    string
		absent  string
		present string
	}{
		{
			name:    "i64-only",
			src:     "function main(): i32 { var n: i64 = 7 as i64; write(n.to_string() + \"\\n\"); return 0; }",
			want:    "7\n",
			present: "$__fern_i64_to_str",
			absent:  "$__fern_i32_to_str",
		},
		{
			name:    "u64-only",
			src:     "function main(): i32 { var n: u64 = 9 as u64; write(n.to_string() + \"\\n\"); return 0; }",
			want:    "9\n",
			present: "$__fern_u64_to_str",
			absent:  "$__fern_i64_to_str",
		},
		{
			name:    "i32-only",
			src:     "function main(): i32 { var n: i32 = 5; write(n.to_string() + \"\\n\"); return 0; }",
			want:    "5\n",
			present: "$__fern_i32_to_str",
			absent:  "$__fern_i64_to_str",
		},
	} {
		t.Run("gate-"+tc.name, func(t *testing.T) {
			// The hand-written formatters and their gates are the AST lowering's.
			wat := string(runCaptureAST(t, runner, driverBin, []byte(tc.src), "-ir"))
			if !strings.Contains(wat, "(func "+tc.present) {
				t.Errorf("emitted wat has no %s body", tc.present)
			}
			if strings.Contains(wat, "(func "+tc.absent) {
				t.Errorf("emitted wat carries the unused %s body", tc.absent)
			}
			if got := runWAT(t, tc.name, emit(t, tc.src)); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
			}
		})
	}

	// A zero formatted through the i64 body is boxed by $__fern_str_box like
	// every other string block: its early-return branch used to hand back a
	// header-less $__fern_alloc block, which $__fern_arr_dec would have freed
	// off a garbage rc word. The churn loop reclaims one per iteration, so a
	// corrupted freelist shows up as a trap rather than passing by luck.
	t.Run("zero-churn", func(t *testing.T) {
		src := `function main(): i32 {
    var n: i64 = 0 as i64;
    var total: i32 = 0;
    var i: i32 = 0;
    while (i < 20000) {
        var s: string = n.to_string();
        total = (total + s.len()) % 251;
        i = i + 1;
    }
    write(total.to_string() + "\n");
    return 0;
}
`
		if got := runWAT(t, "zero-churn", emit(t, src)); got != "171\n" {
			t.Errorf("i64 zero churn = %q, want %q", got, "171\n")
		}
	})
}
