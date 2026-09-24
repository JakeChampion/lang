package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// A callee lent a string VIEW that hands the box straight back (#9328). The
// view's box carries the immortal rc sentinel, so the result is not a unit of
// the caller's own; the semantic lowering hands such a callee a copy, which
// its result then owns. Before that the frame that sliced the view and the
// holder of the result released one box twice, which the sanitizer reports
// deterministically. `dup` is the fresh-result control.
const lentViewHandbackSrc = `function keep(text: string): string { return text; }
function dup(text: string): string { return text + "!"; }
function scan(src: string): string {
    var v: str = slice_unchecked(src, 0, 3);
    return keep(v);
}
function scan_dup(src: string): string {
    var v: str = slice_unchecked(src, 0, 3);
    return dup(v);
}
function main(): i32 { return scan("12345 abc").len() * 10 + scan_dup("12345 abc").len(); }
`

func TestSelfHostLentViewHandback(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "lent_view_handback.fern")
	if err := os.WriteFile(src, []byte(lentViewHandbackSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_SEM_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != 34 {
		t.Fatalf("exit=%d, want 34 (stderr %q)", exit, stderr)
	}
	assertBalancedCensus(t, stderr)
}
