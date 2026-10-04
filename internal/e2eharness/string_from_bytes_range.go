package e2eharness

import (
	"regexp"
	"testing"
)

// StringFromBytesRangeProgram checks string_from_bytes_range_unchecked
// against string_from_bytes_unchecked of the same bytes: every range of a
// 40-byte source, so lengths cross the inline-string and word boundaries;
// the empty range at each end; bytes that are not UTF-8; and a source that is
// changed after the copy, which the string must not see. It exits 0, or the
// number of the first check that failed.
const StringFromBytesRangeProgram = `function copied(b: u8[], from: i32, end: i32): string {
    let out: u8[] = __alloc_u8(end - from);
    let i: i32 = 0;
    while (from + i < end) { out = out.with(i, b[from + i]); i = i + 1; }
    return string_from_bytes_unchecked(out);
}
function main(): i32 {
    let b: u8[] = __alloc_u8(40);
    let i: i32 = 0;
    while (i < b.len()) { b = b.with(i, (65 + i) as u8); i = i + 1; }
    let from: i32 = 0;
    while (from <= b.len()) {
        let end: i32 = from;
        while (end <= b.len()) {
            let s: string = string_from_bytes_range_unchecked(b, from, end);
            if (s.len() != end - from) { return 1; }
            if (s != copied(b, from, end)) { return 2; }
            end = end + 1;
        }
        from = from + 1;
    }
    let raw: u8[] = [0u8, 255u8, 128u8, 10u8];
    let odd: string = string_from_bytes_range_unchecked(raw, 1, 3);
    if (odd.len() != 2 || odd != string_from_bytes_unchecked([255u8, 128u8])) { return 3; }
    let kept: string = string_from_bytes_range_unchecked(b, 2, 30);
    b = b.with(5, 33 as u8);
    if (kept != "CDEFGHIJKLMNOPQRSTUVWXYZ[\\]^") { return 4; }
    if (string_from_bytes_range_unchecked(b, 5, 6) != "!") { return 5; }
    return 0;
}`

// StringFromBytesRangeTraps are the ranges of a six-byte source that trap the
// way an array slice does, one per clause of the bounds check: a negative
// start, an end past the source, and an end before the start.
var StringFromBytesRangeTraps = []struct{ Name, From, End string }{
	{"negative start", "0 - 1", "1"},
	{"end past the source", "0", "7"},
	{"end before start", "4", "2"},
}

// StringFromBytesRangeTrapProgram copies bytes[from, end) of a six-byte source.
func StringFromBytesRangeTrapProgram(from, end string) string {
	return `function main(): i32 {
    let b: u8[] = [97u8, 98u8, 99u8, 100u8, 101u8, 102u8];
    let s: string = string_from_bytes_range_unchecked(b, ` + from + `, ` + end + `);
    print(s);
    return 0;
}`
}

var leakcheckLine = regexp.MustCompile(`(?m)^leakcheck: allocs=([0-9]+) frees=([0-9]+) live_bytes=([0-9]+)$`)

// CheckLeakcheckBalanced fails unless out carries a FERN_LEAKCHECK summary
// with as many frees as allocations and no live bytes.
func CheckLeakcheckBalanced(t *testing.T, out string) {
	t.Helper()
	m := leakcheckLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no leakcheck summary in output:\n%s", out)
	}
	if m[1] != m[2] || m[3] != "0" {
		t.Fatalf("leakcheck unbalanced: allocs=%s frees=%s live_bytes=%s", m[1], m[2], m[3])
	}
}
