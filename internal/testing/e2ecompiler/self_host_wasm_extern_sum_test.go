package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostWasmExternSum(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("wasm_extern_sum_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_extern_sum_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_extern_sum_run.fern", "wasm_extern_sum_run")

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
