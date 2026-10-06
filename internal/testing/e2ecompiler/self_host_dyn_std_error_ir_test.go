package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// dynStdErrorSrc propagates a concrete error through `?` into a
// `Result[i32, dyn error.Error]` and calls `message()` on the boxed value.
// The typed lowering refused the call (`dyn method has no implementation:
// message`, #10765) before an impl record carried its trait's module
// (#10844). handler(true) is Ok(43); handler(false)'s message is "missing".
const dynStdErrorSrc = `import "std/error" as error;
import "std/i32";
struct NotFound { what: string }
impl error.Error for NotFound { function message(self: Self): string { return self.what; } }
function find(ok: boolean): Result[i32, NotFound] {
    if (ok) { return Ok(42); }
    return Err(NotFound { what: "miss" + "ing" });
}
function handler(ok: boolean): Result[i32, dyn error.Error] {
    let v: i32 = find(ok)?;
    return Ok(v + 1);
}
function main(): i32 {
    let a: i32 = match (handler(true)) { Ok(v) => v, Err(e) => 0 };
    let b: string = match (handler(false)) { Ok(v) => "", Err(e) => e.message() };
    print(a.to_string() + ":" + b);
    return 0;
}
`

func dynStdErrorWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, dynStdErrorSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostDynStdErrorIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynStdErrorWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", dynStdErrorSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", dynStdErrorSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostDynStdErrorIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := dynStdErrorWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", dynStdErrorSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostDynStdErrorWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynStdErrorWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", dynStdErrorSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", dynStdErrorSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
