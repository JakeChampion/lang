package x86_64ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// scanResult builds main() = <helper>(literal, args...) and returns the exit
// code the real binary produces. Exit codes are a byte, so every expectation
// below stays in 0..255 — the miss sentinel -1 arrives as 255.
func scanResult(t *testing.T, helper, lit string, args ...int64) int {
	t.Helper()
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	vals := []ssa.Value{constStr(f, e, lit)}
	for _, a := range args {
		vals = append(vals, constOp(f, e, a))
	}
	f.SetRet(e, callOp(f, e, helper, vals...))
	return assembleRunModule(t, map[string]*ssa.Func{"main": f}, "main", 8, nil)
}

// __fern_memchr(s, byte, from): first match at or after `from`, else -1.
func TestAsmRunMemchr(t *testing.T) {
	const s = "abcabc"
	for _, c := range []struct {
		name    string
		b, from int64
		want    int
	}{
		{"first", 'b', 0, 1},
		{"from-skips-first", 'b', 2, 4},
		{"from-past-match", 'b', 5, 255},
		{"absent", 'z', 0, 255},
		{"from-negative-clamps-to-0", 'a', -9, 0},
		{"from-past-end", 'a', 99, 255},
		{"byte-out-of-range", 300, 0, 255},
		{"byte-negative", -1, 0, 255},
		{"last-index", 'c', 0, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scanResult(t, "__fern_memchr", s, c.b, c.from); got != c.want {
				t.Errorf("memchr(%q, %d, %d) = %d, want %d", s, c.b, c.from, got, c.want)
			}
		})
	}
	if got := scanResult(t, "__fern_memchr", "", 'a', 0); got != 255 {
		t.Errorf("memchr on the empty string = %d, want -1", got)
	}
}

// __fern_rmemchr(s, byte, from): LAST match at or before `from`, else -1. The
// clamp is memchr's mirrored — `from` past the end clamps DOWN to len-1, where
// memchr's past-the-end is a miss.
func TestAsmRunRmemchr(t *testing.T) {
	const s = "abcabc"
	for _, c := range []struct {
		name    string
		b, from int64
		want    int
	}{
		{"last", 'b', 5, 4},
		{"from-limits-search", 'b', 3, 1},
		{"from-on-the-match", 'b', 4, 4},
		{"from-before-any", 'b', 0, 255},
		{"absent", 'z', 5, 255},
		{"from-past-end-clamps-down", 'c', 99, 5},
		{"from-negative-is-a-miss", 'a', -1, 255},
		{"byte-out-of-range", 300, 5, 255},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scanResult(t, "__fern_rmemchr", s, c.b, c.from); got != c.want {
				t.Errorf("rmemchr(%q, %d, %d) = %d, want %d", s, c.b, c.from, got, c.want)
			}
		})
	}
	if got := scanResult(t, "__fern_rmemchr", "", 'a', 0); got != 255 {
		t.Errorf("rmemchr on the empty string = %d, want -1 (the clamp yields -1)", got)
	}
}

// __fern_ascii_run(s, from): first byte at or after `from` with the high bit
// set, or len(s). The miss answer is the LENGTH, not -1.
func TestAsmRunAsciiRun(t *testing.T) {
	for _, c := range []struct {
		name string
		s    string
		from int64
		want int
	}{
		{"all-ascii-returns-len", "abcd", 0, 4},
		{"high-bit-at-2", "ab\xc3\xa9", 0, 2},
		{"from-past-the-high-byte", "ab\xc3\xa9", 3, 3},
		{"from-negative-clamps-to-0", "\x80bc", -5, 0},
		{"from-past-end-returns-len", "abc", 99, 3},
		{"empty", "", 0, 0},
		{"0x7f-is-ascii", "\x7f\x7f", 0, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scanResult(t, "__fern_ascii_run", c.s, c.from); got != c.want {
				t.Errorf("ascii_run(%q, %d) = %d, want %d", c.s, c.from, got, c.want)
			}
		})
	}
}

// __fern_count_byte(s, byte): a population, so both degenerate cases are 0
// rather than a sentinel.
func TestAsmRunCountByte(t *testing.T) {
	for _, c := range []struct {
		name string
		s    string
		b    int64
		want int
	}{
		{"three", "banana", 'a', 3},
		{"one", "banana", 'b', 1},
		{"absent", "banana", 'z', 0},
		{"empty", "", 'a', 0},
		{"byte-out-of-range", "banana", 300, 0},
		{"byte-negative", "banana", -1, 0},
		{"every-byte", "aaaa", 'a', 4},
		{"high-byte", "a\xffb\xff", 0xff, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := scanResult(t, "__fern_count_byte", c.s, c.b); got != c.want {
				t.Errorf("count_byte(%q, %d) = %d, want %d", c.s, c.b, got, c.want)
			}
		})
	}
}
