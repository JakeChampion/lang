package coreutils

import (
	"runtime"
	"strings"
	"testing"
)

// diffBody is what every parity failure in this package prints, so its two
// jobs are worth pinning: a short difference must still come out whole, and
// a long one must not. The second is why it exists — quoting two multi-
// kilobyte streams on one line wedged the macOS lane twice (#9703).
func TestDiffBodyPrintsShortStreamsWhole(t *testing.T) {
	got := diffBody("gnu", "fern", []byte("abc\n"), []byte("abd\n"))
	for _, want := range []string{`gnu: "abc\n"`, `fern: "abd\n"`} {
		if !strings.Contains(got, want) {
			t.Errorf("diffBody lost a short stream: want %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "bytes before") || strings.Contains(got, "first differing") {
		t.Errorf("a short difference must print whole, with no window:\n%s", got)
	}
}

func TestDiffBodyBoundsLongStreams(t *testing.T) {
	left := append(repeatByte('a', 4000), "LEFT"...)
	right := append(repeatByte('a', 4000), "RIGHT"...)
	got := diffBody("gnu", "fern", left, right)

	if len(got) > 2*(2*diffContext+120) {
		t.Errorf("diffBody is %d bytes for two 4000-byte streams — the point is that it is bounded:\n%s", len(got), got)
	}
	if !strings.Contains(got, "first differing at byte 4000") {
		t.Errorf("diffBody must name where the two part:\n%s", got)
	}
	if !strings.Contains(got, "4004 and 4005 bytes") {
		t.Errorf("diffBody must give both lengths, which the window hides:\n%s", got)
	}
	if !strings.Contains(got, "LEFT") || !strings.Contains(got, "RIGHT") {
		t.Errorf("the window must cover the difference itself:\n%s", got)
	}
	if !strings.Contains(got, "[3904 bytes before]") {
		t.Errorf("what was dropped must be counted rather than silently elided:\n%s", got)
	}
}

// A difference at the end of the shorter stream is the one case where the two
// agree everywhere they overlap, so firstDiff has to answer the shorter length
// rather than walking off it.
func TestDiffBodyHandlesOneStreamBeingAPrefix(t *testing.T) {
	left := repeatByte('x', 600)
	right := append(repeatByte('x', 600), 'y')
	got := diffBody("native", "wasm", left, right)
	if !strings.Contains(got, "first differing at byte 600") {
		t.Errorf("a prefix must part at its own end:\n%s", got)
	}
	if !strings.Contains(got, "600 and 601 bytes") {
		t.Errorf("diffBody must give both lengths:\n%s", got)
	}
}

// The window has to be chosen BEFORE either side is quoted, which a test
// reading the message cannot tell: quoting first and discarding the result
// returns the same string and allocates the whole stream anyway. So this
// measures instead. The real case is Darwin's `printf "%.99999999999d" 1`,
// which answers 1.2 GB of zeros.
func TestDiffBodyDoesNotQuoteWhatItWindowsAway(t *testing.T) {
	const size = 64 << 20
	left := repeatByte('a', size)
	right := repeatByte('a', size)
	right[size-1] = 'b'

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := diffBody("gnu", "fern", left, right)
	runtime.ReadMemStats(&after)

	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Errorf("diffBody allocated %d bytes for two %d-byte streams — it quoted what it then threw away", grew, size)
	}
	if !strings.Contains(got, "first differing at byte 67108863") {
		t.Errorf("diffBody must still find the difference:\n%s", got)
	}
}

func repeatByte(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}
