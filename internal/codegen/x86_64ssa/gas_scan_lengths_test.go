package x86_64ssa

import (
	"fmt"
	"strings"
	"testing"
)

// The byte-scan kernels are SSE2, 16 bytes an iteration, with a scalar tail —
// so the interesting inputs are the ones near a block boundary, and the cases
// in gas_scan_test.go all use a six-byte string that never reaches the vector
// loop at all. This sweeps every length across two blocks and every needle
// position within each, which is what actually exercises: the 16-byte
// threshold, a hit in the first block, a hit in a later block, a hit in the
// scalar tail after whole blocks, and — for the backward kernel — the block
// arithmetic that must not skip the byte between two blocks.
//
// The oracle is strings.IndexByte / LastIndexByte rather than a second copy of
// the arithmetic, so a shared misunderstanding cannot make both agree.

// scanLengths is 0..40: past two 16-byte blocks, and past every boundary the
// forward and backward kernels round against.
func scanLengths() []int {
	out := make([]int, 0, 41)
	for n := 0; n <= 40; n++ {
		out = append(out, n)
	}
	return out
}

func TestAsmRunMemchrAcrossLengths(t *testing.T) {
	const needle = 'x'
	for _, n := range scanLengths() {
		base := strings.Repeat("a", n)
		// The miss: no needle anywhere.
		if got, want := scanResult(t, "__fern_memchr", base, needle, 0), 255; got != want {
			t.Errorf("len %d, no needle: got %d, want %d (miss)", n, got, want)
		}
		for pos := 0; pos < n; pos++ {
			s := base[:pos] + string(rune(needle)) + base[pos+1:]
			want := strings.IndexByte(s, needle)
			if got := scanResult(t, "__fern_memchr", s, needle, 0); got != want {
				t.Errorf("len %d, needle at %d, from 0: got %d, want %d", n, pos, got, want)
			}
			// Starting ON the needle must find it; starting one past must not
			// find it at this position. Together these pin the `from` clamp
			// against the vector loop's cursor rather than only the scalar one.
			if got := scanResult(t, "__fern_memchr", s, needle, int64(pos)); got != pos {
				t.Errorf("len %d, needle at %d, from %d: got %d, want %d", n, pos, pos, got, pos)
			}
			after := strings.IndexByte(s[pos+1:], needle)
			wantAfter := 255
			if after >= 0 {
				wantAfter = pos + 1 + after
			}
			if got := scanResult(t, "__fern_memchr", s, needle, int64(pos+1)); got != wantAfter {
				t.Errorf("len %d, needle at %d, from %d: got %d, want %d", n, pos, pos+1, got, wantAfter)
			}
		}
	}
}

func TestAsmRunRmemchrAcrossLengths(t *testing.T) {
	const needle = 'x'
	for _, n := range scanLengths() {
		base := strings.Repeat("a", n)
		from := int64(n) // clamps down to len-1, i.e. search the whole string
		if got, want := scanResult(t, "__fern_rmemchr", base, needle, from), 255; got != want {
			t.Errorf("len %d, no needle: got %d, want %d (miss)", n, got, want)
		}
		for pos := 0; pos < n; pos++ {
			s := base[:pos] + string(rune(needle)) + base[pos+1:]
			want := strings.LastIndexByte(s, needle)
			if got := scanResult(t, "__fern_rmemchr", s, needle, from); got != want {
				t.Errorf("len %d, needle at %d, from %d: got %d, want %d", n, pos, from, got, want)
			}
			// One BELOW the needle must miss it. This is the case that catches
			// a backward block stride that skips the byte between two blocks.
			wantBelow := 255
			if pos > 0 {
				if b := strings.LastIndexByte(s[:pos], needle); b >= 0 {
					wantBelow = b
				}
			}
			if got := scanResult(t, "__fern_rmemchr", s, needle, int64(pos-1)); got != wantBelow {
				t.Errorf("len %d, needle at %d, from %d: got %d, want %d", n, pos, pos-1, got, wantBelow)
			}
		}
	}
}

// Two needles, so the forward kernel must return the FIRST and the backward one
// the LAST — a kernel that returns any match passes the single-needle sweep.
func TestAsmRunScanPicksTheRightEndAcrossBlocks(t *testing.T) {
	const needle = 'x'
	for _, n := range []int{16, 17, 31, 32, 33, 40} {
		for _, lo := range []int{0, 1, 15, 16} {
			for _, hi := range []int{n - 1, n - 2, 16, 17} {
				if lo >= hi || hi >= n {
					continue
				}
				b := []byte(strings.Repeat("a", n))
				b[lo], b[hi] = needle, needle
				s := string(b)
				name := fmt.Sprintf("len%d_lo%d_hi%d", n, lo, hi)
				if got := scanResult(t, "__fern_memchr", s, needle, 0); got != lo {
					t.Errorf("%s: memchr got %d, want %d", name, got, lo)
				}
				if got := scanResult(t, "__fern_rmemchr", s, needle, int64(n)); got != hi {
					t.Errorf("%s: rmemchr got %d, want %d", name, got, hi)
				}
			}
		}
	}
}
