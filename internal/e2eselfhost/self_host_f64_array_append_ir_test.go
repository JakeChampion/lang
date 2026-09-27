package e2eselfhost

import "testing"

// f64ArrayAppendIRCases close the f64[] `.append(v)` gap on the IR path: an
// `a = a.append(x)` on an `f64[]` local lowers, like the i64[]/u64[] appends
// via arr_push_i64. The f64 value is carried on the IR value stack as
// raw 8 bytes, so the register backends reuse __fern_arr_push exactly like the
// i64 path; wasm32 (typed operand stack) uses an f64-typed $__fern_arr_push_f64
// companion to $__fern_arr_push_i64. The element-read side (a[i] via f64.load,
// for-in, .len, .with) already lowered, so this closes the write side.
//
// Each case is oracle-checked against the interpreter and returns a value
// <= 126 (cf. the wasmtime exit-code gap #2908).
var f64ArrayAppendIRCases = []struct {
	name string
	main string
}{
	// Append to a non-empty f64[], then read the length.
	{"f64-append-len", `function main(): i32 { var a: f64[] = [1.0]; a = a.append(2.0); a = a.append(3.0); return a.len(); }`},
	// Append to a non-empty f64[], then index the appended element (value round-trip).
	{"f64-append-index", `function main(): i32 { var a: f64[] = [1.5]; a = a.append(2.5); return a[1] as i32; }`},
	// Append onto an EMPTY f64[] literal (the geometric-growth first-alloc path).
	{"f64-append-empty-sum", `function main(): i32 { var a: f64[] = []; a = a.append(1.5); a = a.append(2.5); var s = 0.0; for x in a { s = s + x; } return s as i32; }`},
	// Repeated appends past the initial capacity (forces the grow-and-copy path).
	{"f64-append-grow-many", `function main(): i32 { var a: f64[] = []; var i = 0; while (i < 10) { a = a.append((i as f64) + 0.5); i = i + 1; } return a[7] as i32; }`},
	// i64[] append regression — must stay on the IR path (shares __fern_arr_push).
	{"i64-append-regress", `function main(): i32 { var a: i64[] = [1]; a = a.append(2); return a[1] as i32; }`},
}

// TestSelfHostF64ArrayAppendIR compiles each case with the self-host CLI for
// x86-64 and wasm and checks the exit code against the interpreter.
func TestSelfHostF64ArrayAppendIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range f64ArrayAppendIRCases {
		src := tc.main + "\n"
		want := interpExit(t, interpBin, src)
		for _, target := range []string{"x86-64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOf(t, src, target); code != want {
					t.Errorf("exited %d, want %d (interp oracle)\n%s", code, want, stderr)
				}
			})
		}
	}
}
