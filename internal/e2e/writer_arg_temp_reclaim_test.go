package e2e

import "testing"

// A fresh string passed straight to Writer.write is released after the call
// (#8413), and so is the write's own Option[IoError] result box, which is one
// counted rc=1 block per call since #8405. A Writer is a handle with no box,
// so nothing is left unpaired, whatever the round count — before #8413 the
// build() results alone left two per round.
const writerArgTempSrc = `function build(n: i32): string {
    let out: string = "";
    let i: i32 = 0;
    while (i < 3) { out = out + "abcdefgh"; i = i + 1; }
    return out;
}
function main(): i32 {
    let w: Writer = stdout();
    let k: i32 = 0;
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
	// The 40 build() results and the 40 write result boxes all come back.
	if got := allocs - frees; got != 0 {
		t.Errorf("allocs=%d frees=%d: %d unpaired, want 0 — a per-round temp is not released", allocs, frees, got)
	}
}
