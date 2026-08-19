package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostWasmExternSum pins the wasm backend's flat-sum extern type checks
// (examples/self_host/wasm_ir.fern's extern_sum_param_supported /
// extern_sum_param_is_option — SH-021, docs/SELF-HOST-AUDIT.md T2). Both now
// decode an Option[…] / Result[…, …] spelling via the structured TypeRef
// (parser.parse_type_ref) instead of the magic-byte `Option[` / `Result[`
// prefix + top-level-comma depth scan.
//
// The wasm_extern_sum_run driver runs the REAL (imported) functions against a
// frozen copy of the FORMER magic-byte logic over a corpus spanning every branch
// (Option/Result of i32/u32 vs of string/struct/nested-generic; single/two/three-
// arg arities; arrays incl. Option[i32][]; the degenerate Option[]; bare
// Option/Result; plain scalars/structs), and exits with the mismatch count. The
// golden below (all "ok", mismatches=0) is the byte-identical guard: because the
// two are pure boolean functions, identical output over this corpus proves the
// wasm codegen is unchanged, so a regression in the TypeRef decode (or in
// parse_type_ref feeding it) fails here rather than silently mis-classifying a
// WIT extern param.
//
// The driver is built natively via the Go x86-64 backend; its stdout is the map.
func TestSelfHostWasmExternSum(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("wasm_extern_sum_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_extern_sum_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "wasm_extern_sum_run.fern", "wasm_extern_sum_run")

	const want = "ok  Option[i32] sum=1 opt=1\n" +
		"ok  Option[u32] sum=1 opt=1\n" +
		"ok  Option[string] sum=0 opt=1\n" +
		"ok  Option[Foo] sum=0 opt=1\n" +
		"ok  Option[Map[a, b]] sum=0 opt=1\n" +
		"ok  Option[i32][] sum=0 opt=1\n" +
		"ok  Option[] sum=0 opt=1\n" +
		"ok  Option sum=0 opt=0\n" +
		"ok  Result[i32, u32] sum=1 opt=0\n" +
		"ok  Result[u32, i32] sum=1 opt=0\n" +
		"ok  Result[i32, string] sum=0 opt=0\n" +
		"ok  Result[string, i32] sum=0 opt=0\n" +
		"ok  Result[i32] sum=0 opt=0\n" +
		"ok  Result[i32, u32, x] sum=0 opt=0\n" +
		"ok  Result[i32, u32][] sum=0 opt=0\n" +
		"ok  Result sum=0 opt=0\n" +
		"ok  Result[Map[a, b], i32] sum=0 opt=0\n" +
		"ok  i32 sum=0 opt=0\n" +
		"ok  u32 sum=0 opt=0\n" +
		"ok  string sum=0 opt=0\n" +
		"ok  Foo sum=0 opt=0\n" +
		"ok  u8[] sum=0 opt=0\n" +
		"ok   sum=0 opt=0\n" +
		"ok  boolean[] sum=0 opt=0\n" +
		"ok  Map[string, i32] sum=0 opt=0\n" +
		"mismatches=0\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("wasm_extern_sum_run did not exit normally")
	}
	if got := string(out); got != want {
		t.Errorf("wasm extern-sum decode mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm_extern_sum_run exit code = %d, want 0 (byte-identity mismatches)", code)
	}
}
