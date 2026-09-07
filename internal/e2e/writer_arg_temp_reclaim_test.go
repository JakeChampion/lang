package e2e

import "testing"

// A fresh string passed straight to Writer.write is released after the call
// (#8413), and so is the write's own Option[IoError] result box, which is one
// counted rc=1 block per call since #8405. What is left unpaired is the
// per-STREAM handle `stdout()` builds: it is sentinel-headered and deliberately
// still immortal (#8398's remaining half), and it is allocated once, not per
// round. So the count this pins is exactly one, whatever the round count —
// before #8413 the build() results alone made it two per round.
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
	// The stdout() handle is the one unpaired block; the 40 build() results
	// and the 40 write result boxes must not join it.
	if got := allocs - frees; got != 1 {
		t.Errorf("allocs=%d frees=%d: %d unpaired, want 1 (the stdout handle alone) — a per-round temp is not released", allocs, frees, got)
	}
}
