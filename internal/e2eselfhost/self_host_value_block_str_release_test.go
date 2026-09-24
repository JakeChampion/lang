package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A string local bound from an if- or match-expression is released on the AST
// lowering (#10216). No reclaim credit reached a binding whose initializer is
// the value-block desugar, so the fresh string it held leaked once per
// evaluation, including the one bound in a loop body. The credit goes by the
// block's live leaves: all freshly allocated, or fresh and literal (the
// literal-local gate), with a diverging branch contributing none.
const valueBlockStrReleaseSrc = `import "std/i32";
function fresh(n: i32): i32 { var s: string = if (n > 0) { "pos" + n.to_string() } else { "neg" + n.to_string() }; return s.len(); }
function mixed(n: i32): i32 { var s: string = if (n > 0) { "pos" + n.to_string() } else { "x" }; return s.len(); }
function early(n: i32): i32 { var s: string = if (n > 0) { "pos" + n.to_string() } else { return 0; }; return s.len(); }
function arms(n: i32): i32 {
    var s: string = match (n) { 0 => "zero" + "", 1 => "one" + n.to_string(), _ => { return 9; } };
    return s.len();
}
function looped(k: i32): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < k) {
        var s: string = if (i % 2 == 0) { "e" + i.to_string() } else { "o" };
        t = t + s.len();
        i = i + 1;
    }
    return t;
}
function main(): i32 {
    return fresh(12) + fresh(0 - 3) + mixed(12) + mixed(0) + early(12) + early(0) + arms(0) + arms(1) + arms(5) + looped(20);
}
`

const valueBlockStrReleaseWant = 73

func TestSelfHostValueBlockStrRelease(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "value_block_str_release.fern")
	if err := os.WriteFile(src, []byte(valueBlockStrReleaseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_SEM_IR=", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != valueBlockStrReleaseWant {
		t.Fatalf("exit=%d, want %d (stderr %q)", exit, valueBlockStrReleaseWant, stderr)
	}
	assertBalancedCensus(t, stderr)
}
