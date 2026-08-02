package e2eselfhost

import (
	"testing"
)

// TestSelfHostJoinIR pins `xs.join(sep)` on a string[] receiver lowering through
// the self-host x86-64 IR path (#5328). Before this, irlower never intercepted
// `.join`, so any module using it fell back to the legacy AST emitter; now it
// lowers to a call of the __fern_arr_str_join runtime helper (the same Fern
// function the AST path calls). Each case is a native-oracle exit-code
// differential: the native interpreter (which reaches `.join` via std/array's
// __method_Array_join) is the source of truth, and the self-host-IR-compiled
// binary must match. The single-program `asm_ir_run -ir` driver resolves no
// stdlib and treats `.join` as a builtin, so the self-host source omits the
// import the native oracle needs — the prepended `import "std/array";` is the
// only difference between the two.
func TestSelfHostJoinIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cases := []struct {
		name string
		// body is the program WITHOUT the std/array import (self-host builtin);
		// the native oracle runs it with the import prepended.
		body string
	}{
		{
			// 3 elements + a "-" separator: len("a-bb-ccc") = 8.
			"multi",
			`function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("bb"); xs = xs.append("ccc"); return xs.join("-").len(); }`,
		},
		{
			// Empty array joins to "" (len 0) — the n==0 accumulator path.
			"empty",
			`function main(): i32 { var xs: string[] = []; return xs.join(",").len(); }`,
		},
		{
			// A single element has no separator applied: len("solo") = 4.
			"single",
			`function main(): i32 { var xs: string[] = []; xs = xs.append("solo"); return xs.join(",").len(); }`,
		},
		{
			// Empty separator concatenates directly: len("abc") = 3.
			"empty-sep",
			`function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); xs = xs.append("c"); return xs.join("").len(); }`,
		},
		{
			// A multi-char separator: len("x - y - z") = 9.
			"multi-char-sep",
			`function main(): i32 { var xs: string[] = []; xs = xs.append("x"); xs = xs.append("y"); xs = xs.append("z"); return xs.join(" - ").len(); }`,
		},
		{
			// The join result feeds `+` concat (exercises expr_is_str tracking of a
			// `.join` result): len("[a,b]") = 5.
			"concat-result",
			`function main(): i32 { var xs: string[] = []; xs = xs.append("a"); xs = xs.append("b"); var s: string = "[" + xs.join(",") + "]"; return s.len(); }`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := runInterpExit(t, "import \"std/array\";\n"+tc.body)
			got := compileAndRunSelfHostIR(t, gcc, runner, dir, driverBin, "join_"+tc.name, tc.body)
			if got != want {
				t.Errorf("%s: self-host IR exited %d, native oracle %d", tc.name, got, want)
			}
		})
	}
}
