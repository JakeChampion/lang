package e2eselfhost

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A string projection returned out of a frame keeps a counted unit of its own
// after the parent array is released (#8999). join_range hands back
// `parts[lo]` for a single chunk, and joined drops `parts` on the way out;
// without a unit of its own the result was freed there, and the next
// allocation, churn's, overwrote it. The typed lowering supplies the unit;
// the AST lowering still returns the freed buffer.
const stringProjectionReturnSrc = `import "std/i32";

@noinline function joined(n: i32): string {
    var parts: string[] = [];
    parts = parts.append("runtime-record-field-" + n.to_string());
    return join_range(parts, 0, parts.len());
}
@noinline function churn(n: i32): string {
    return "different-field-data-" + n.to_string();
}
function main(): i32 {
    var result = joined(1);
    var i = 0;
    while (i < 64) {
        var replacement = churn(i);
        if (replacement.len() == 0) { return 2; }
        if (result != "runtime-record-field-1") { print(result); return 1; }
        i = i + 1;
    }
    return 0;
}
@noinline function join_range(parts: string[], lo: i32, hi: i32): string {
    var n: i32 = hi - lo;
    if (n <= 0) { return ""; }
    if (n == 1) { return parts[lo]; }
    var mid: i32 = lo + n / 2;
    return join_range(parts, lo, mid) + join_range(parts, mid, hi);
}
`

func TestSelfHostStringProjectionReturnOutlivesItsParent(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeEnumMapSrc(t, "string_projection_return", stringProjectionReturnSrc)
	t.Run("x86-64", func(t *testing.T) {
		bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
		stderr, exit := runWithStdin(t, cli.runner, bin, nil)
		if exit != 0 {
			t.Fatalf("exit = %d, want 0\n%s", exit, stderr)
		}
		assertBalancedCensus(t, stderr)
	})
	t.Run("arm64", func(t *testing.T) {
		armgcc, qemu := arm64Tooling(t)
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "string_projection_return", string(asm)))
		var eb strings.Builder
		cmd.Stderr = &eb
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 0 {
			t.Fatalf("exit = %d, want 0\n%s", code, eb.String())
		}
		assertBalancedCensus(t, eb.String())
	})
	t.Run("wasm", func(t *testing.T) {
		if _, err := exec.LookPath("wasmtime"); err != nil {
			t.Skip("wasmtime not on PATH")
		}
		wat := cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", "FERN_SEM_IR=1")
		stderr, exit := runWasmCensus(t, wat)
		if exit != 0 {
			t.Fatalf("exit = %d, want 0\n%s", exit, stderr)
		}
		assertBalancedCensus(t, stderr)
	})
}
