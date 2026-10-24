package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

const valueBlockEarlyReturnSrc = `import "std/i32";
function probe(p: string): i32 {
    let si: FileStat = match (stat(p)) { Ok(v) => v, Err(_) => { return 1; } };
    if (si.is_dir) { return 4; }
    return 16;
}
function mine(p: string): Result[FileStat, string] {
    match (stat(p)) { Ok(v) => { return Ok(v); }, Err(_) => { return Err("no"); } }
}
function wrapped(p: string): i32 {
    let si: FileStat = match (mine(p)) { Ok(v) => v, Err(_) => { return 1; } };
    if (si.is_dir) { return 4; }
    return 16;
}
function label(n: i32): string {
    let s: string = if (n > 0) { "pos" + n.to_string() } else { return "neg"; };
    return s + "!";
}
function main(): i32 {
    return probe("/") + probe("/nonexistent-9326") + wrapped("/") * 8 + wrapped("/nonexistent-9326") * 8
        + label(12).len() * 2 + label(0 - 1).len();
}
`

const valueBlockEarlyReturnWant = 5 + 40 + 12 + 3

func TestSelfHostValueBlockEarlyReturn(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "value_block_early_return.fern")
	if err := os.WriteFile(src, []byte(valueBlockEarlyReturnSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_LEAKCHECK=1", "FERN_STRICT_IR=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != valueBlockEarlyReturnWant {
		t.Fatalf("exit=%d, want %d (stderr %q)", exit, valueBlockEarlyReturnWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
