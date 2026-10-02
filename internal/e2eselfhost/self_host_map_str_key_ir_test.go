package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A `str` key column (#10761): the column holds an owned copy of each key,
// since a view's box cannot be counted and `keys` would otherwise hand out
// the column's own box. Keys from a split, an owned key, a shared copy, a
// delete, `keys()` and iteration, against the interpreter, with the
// allocations and frees balanced.
const mapStrKeySrc = `import "core/map";
import "std/i32";
function count(m: Map[str, i32], k: str): Map[str, i32] { return m.insert(k, m.get_or(k, 0) + 1); }
function main(): i32 {
    let src: string = "one two one three two one";
    let m: Map[str, i32] = map_new(4);
    for w in src.split(" ") { m = count(m, w); }
    let owned: string = "th" + "ree";
    m = m.insert(owned, 10);
    let shared: Map[str, i32] = m;
    let other: Map[str, i32] = shared.insert("four", 4);
    let r: (Map[str, i32], boolean) = m.without("two");
    let out: string = m.len().to_string() + other.len().to_string() + r.0.len().to_string() + ":";
    let ks: str[] = m.keys();
    let i: i32 = 0;
    while (i < ks.len()) { out = out + ks[i] + "=" + m.get_or(ks[i], 0).to_string() + ","; i = i + 1; }
    for (k, v) in other { if (k == "four") { out = out + k + v.to_string(); } }
    print(out);
    return 0;
}
`

func mapStrKeyWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, mapStrKeySrc)
	if code != 0 || want == "" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostMapStrKeyIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := mapStrKeyWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", mapStrKeySrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", mapStrKeySrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
	quarantine := buildBin(t, cli.gcc, t.TempDir(), "quarantine", cli.emit(t, "x86-64-linux", mapStrKeySrc, "FERN_RC_FREE_DEBUG=1"))
	if stderr, exit := runWithStdin(t, cli.runner, quarantine, nil); exit != 0 {
		t.Fatalf("quarantine run: exit %d, want 0\n%s", exit, stderr)
	}
}

func TestSelfHostMapStrKeyIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := mapStrKeyWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", mapStrKeySrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostMapStrKeyWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	want := mapStrKeyWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", mapStrKeySrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", mapStrKeySrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 0 {
		t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
