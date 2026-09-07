package e2e

import "testing"

// A fresh string passed straight to Writer.write is released after the call
// (#8413), and so is the write's own per-call Option[IoError] box (#8405).
// The stdout() handle is per-stream and stays immortal (#8398), so the shape
// cannot balance; what it pins is the count: exactly that one handle is
// unpaired, and every build() result and write box is freed. Before #8413,
// allocs - frees was two per round; before #8405, one.
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
	// Only the stdout() handle stays unpaired by design; the 40 build()
	// results and the 40 write boxes must not.
	if got := allocs - frees; got != 1 {
		t.Errorf("allocs=%d frees=%d: %d unpaired, want 1 (the handle) — a build() temp or a write box is not released", allocs, frees, got)
	}
}
