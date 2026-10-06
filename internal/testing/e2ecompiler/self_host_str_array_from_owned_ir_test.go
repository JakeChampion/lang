package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// strArrayFromOwnedSrc binds an owned string[] (std/unicode's graphemes and
// words, and a user function's) to a declared str[]. The typed lowering
// retags the array as it retags one string (#10760); the strings stay counted
// through the view type, so the census balances.
const strArrayFromOwnedSrc = `import "std/unicode" as unicode;
import "std/i32";
function parts(n: i32): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < n) { out = out.append("p" + i.to_string()); i = i + 1; }
    return out;
}
function main(): i32 {
    let gs: str[] = unicode.graphemes("abc");
    if (gs.len() != 3) { return 1; }
    if (gs[1] != "b") { return 2; }
    let ws: str[] = unicode.words("one two");
    if (ws.len() != 2) { return 3; }
    if (ws[1] != "two") { return 4; }
    let ps: str[] = parts(4);
    if (ps.len() != 4) { return 5; }
    if (ps[3].len() != 2) { return 6; }
    return 42;
}
`

func TestSelfHostStrArrayFromOwnedIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", strArrayFromOwnedSrc)); code != 42 {
		t.Fatalf("exit %d, want 42 (the code names the failing step)", code)
	}
	bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", strArrayFromOwnedSrc, "FERN_LEAKCHECK=1"))
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}

func TestSelfHostStrArrayFromOwnedWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", strArrayFromOwnedSrc)); code != 42 {
		t.Fatalf("exit %d, want 42 (the code names the failing step)", code)
	}
	census := filepath.Join(t.TempDir(), "census.wat")
	if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", strArrayFromOwnedSrc, "FERN_LEAKCHECK=1")), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr, exit := runWasmCensus(t, census)
	if exit != 42 {
		t.Fatalf("census run: exit %d, want 42\n%s", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
