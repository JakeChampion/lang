package arm64

import (
	"strings"
	"testing"
)

// Exercises every array-buffer helper: the plain grow (i32 self-append), the
// move and non-move element-retaining grows (string[] self-append and append
// into a fresh binding), and the copy-on-write helpers behind `.with` on a
// shared i32[] and a shared string[].
const arrGrowProgram = `function main(): i32 {
    var a: i32[] = [];
    var s: string[] = [];
    var i: i32 = 0;
    while (i < 5) { a = a.append(i); s = s.append("x"); i = i + 1; }
    var b: i32[] = a;
    b = b.with(0, 7);
    var t: string[] = s.append("y");
    var u: string[] = t;
    u = u.with(0, "z");
    return a.len() + b[0] + t.len() + u.len();
}`

// The grow helpers sized their allocation in 32 bits (#8587). The capacity
// doubling `lsl w23, w22, #1` went negative past 2^30 elements, so the
// max(.., 4) floor set cap = 4 under a length near 1e9; `madd w0, ...` wrapped
// cap * stride past a 4 GiB payload. Either way every later store indexed far
// past the buffer. The arithmetic is 64-bit now, and a request past 2^31 - 1
// aborts through __fern_report with the #8457 cause — the same one
// `__alloc_u8` and `__fern_strcat` use.
func TestArrGrowSizesIn64BitsAndRefusesOverflow(t *testing.T) {
	asm := compile(t, arrGrowProgram, Options{})
	// Which element-retaining pair is emitted follows the string ABI; the
	// plain helper is always there.
	names := []string{"__fern_arr_push_grow"}
	elem := 0
	for _, name := range []string{"__fern_arr_push_grow_ptr", "__fern_arr_push_grow_move_ptr", "__fern_arr_push_grow_str", "__fern_arr_push_grow_move_str"} {
		if strings.Contains(asm, "\n"+name+":\n") {
			names = append(names, name)
			elem++
		}
	}
	if elem < 2 {
		t.Fatalf("expected both a move and a non-move element-retaining grow, got %v", names)
	}
	for _, name := range names {
		body := mustHelperSection(t, asm, name)
		for _, want := range []string{"lsl x23, x22, #1", "csel x23, x23, x0, ge", "madd x0, x23, x21, x24", "lsr x1, x0, #31", "_sizebad", "__fern_msg_alloc_size", "mul x2, x20, x21"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, body)
			}
		}
		for _, stale := range []string{"lsl w23, w22, #1", "madd w0, w23, w21, w24", "mul w2, w20, w21"} {
			if strings.Contains(body, stale) {
				t.Errorf("%s still sizes in 32 bits (%q):\n%s", name, stale, body)
			}
		}
	}
}

// The copy-on-write helpers read cap from a header the grow guard already
// accepted, so they need no check of their own — but the product must still
// be taken in 64 bits and handed to __fern_alloc whole.
func TestArrCowSizesIn64Bits(t *testing.T) {
	asm := compile(t, arrGrowProgram, Options{})
	names := []string{"__fern_arr_cow_inplace"}
	elem := 0
	for _, name := range []string{"__fern_arr_cow_inplace_ptr", "__fern_arr_cow_inplace_str"} {
		if strings.Contains(asm, "\n"+name+":\n") {
			names = append(names, name)
			elem++
		}
	}
	if elem == 0 {
		t.Fatal("no element-retaining copy-on-write helper was emitted")
	}
	for _, name := range names {
		body := mustHelperSection(t, asm, name)
		for _, want := range []string{"madd x0, x22, x20, x23", "mul x2, x21, x20"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, body)
			}
		}
		for _, stale := range []string{"madd w0, w22, w20, w23", "mul w2, w21, w20"} {
			if strings.Contains(body, stale) {
				t.Errorf("%s still sizes in 32 bits (%q):\n%s", name, stale, body)
			}
		}
	}
}

// mustHelperSection returns the emitted text of the named runtime helper, from
// its label to its `.size` directive — the whole symbol, including the
// out-of-line abort tail past the last `ret`, which is what runtimeHelperBody's
// first-`ret` cut would drop. Fails the test when the helper was not emitted,
// since an assertion over missing text proves nothing.
func mustHelperSection(t *testing.T, asm, name string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+name+":\n")
	if start < 0 {
		t.Fatalf("%s was not emitted", name)
	}
	end := strings.Index(asm[start:], ".size "+name+",")
	if end < 0 {
		t.Fatalf("%s has no .size directive", name)
	}
	return asm[start : start+end]
}
