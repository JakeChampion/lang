package e2e

import "testing"

// A fresh string passed straight to Writer.write is released after the call
// (#8413). The write's own Option[IoError] result box is sentinel-headered
// and never reclaimed (#8398), so the shape cannot balance; what it pins is
// the count: exactly that one box per round is unpaired, and every build()
// result is freed. Before the release, allocs - frees was two per round.
const writerArgTempSrc = `function build(n: i32): string {
    var out: string = "";
    var i: i32 = 0;
    while (i < 3) { out = out + "abcdefgh"; i = i + 1; }
    return out;
}
function main(): i32 {
    var w: Writer = stdout();
    var k: i32 = 0;
    while (k < 40) {
        match (w.write(build(k))) { Some(_) => { return 1; }, None => {} }
        k = k + 1;
    }
    return 0;
}`

func TestX86_64WriterArgTempReclaimed(t *testing.T) {
	_, stderr, exit := runLeakCheckX86_64(t, writerArgTempSrc)
	if exit != 0 {
		t.Fatalf("exit %d, want 0; stderr: %s", exit, stderr)
	}
	allocs, frees, _ := parseLeakCheckLine(t, stderr)
	if allocs < 120 {
		t.Fatalf("allocs=%d: the probe is not building its strings", allocs)
	}
	// One stdout() handle plus one write result box per round stay unpaired
	// by design; the 40 build() results must not.
	if got := allocs - frees; got != 41 {
		t.Errorf("allocs=%d frees=%d: %d unpaired, want 41 (the handle and one write box per round) — the build() temps are not released", allocs, frees, got)
	}
}
