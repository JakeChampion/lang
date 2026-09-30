package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// dynF32BoxSrc boxes an f32 into a dyn set, beside an i32, over a trait of the
// program's own and over core/cmp's Display. The typed lowering refused the
// box (`array element type: f32 in dyn Show[]`, #10841). 0.1 prints as itself
// only when the value is read back at f32 width.
const dynF32BoxSrc = `import "core/cmp";
import "std/i32";
import "std/float";
trait Show { function show(self: Self): string; }
impl Show for i32 { function show(self: Self): string { return self.to_string(); } }
impl Show for f32 { function show(self: Self): string { return "f" + self.to_string(); } }
function main(): i32 {
    var x: f32 = 2.5;
    var y: f32 = 0.1;
    var xs: dyn Show[] = [41, x, y];
    var out: string = "";
    for d in xs { out = out + d.show() + ";"; }
    var ds: dyn cmp.Display[] = [y, 7];
    for d in ds { out = out + d.to_string() + ";"; }
    print(out);
    return 0;
}
`

func dynF32BoxWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, dynF32BoxSrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostDynF32BoxIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynF32BoxWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", dynF32BoxSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", dynF32BoxSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostDynF32BoxIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := dynF32BoxWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", dynF32BoxSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostDynF32BoxWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := dynF32BoxWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", dynF32BoxSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", dynF32BoxSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
