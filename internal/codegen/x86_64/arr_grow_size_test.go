package x86_64

import (
	"strings"
	"testing"
)

// Exercises every array-buffer helper: the plain grow (i32 self-append), the
// move and non-move pointer-element grows (string[] self-append and append
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
// doubling `shl r15d, 1` went negative past 2^30 elements, so the max(.., 4)
// floor set cap = 4 under a length near 1e9; `imul eax, r13d` wrapped
// cap * stride past a 4 GiB payload. Either way every later store indexed far
// past the buffer. The arithmetic is 64-bit now, and a request past 2^31 - 1
// aborts through __fern_report with the #8457 cause — the same one
// `__alloc_u8` and `__fern_strcat` use.
func TestArrGrowSizesIn64BitsAndRefusesOverflow(t *testing.T) {
	asm := compile(t, arrGrowProgram)
	for _, name := range []string{"__fern_arr_push_grow", "__fern_arr_push_grow_ptr", "__fern_arr_push_grow_move_ptr"} {
		body := mustHelperSection(t, asm, name)
		for _, want := range []string{"shl r15, 1", "cmovl r15, rcx", "cmp rax, 2147483647", "_sizebad", "__fern_msg_alloc_size", "mov rdx, rax"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, body)
			}
		}
		for _, stale := range []string{"shl r15d, 1", "imul eax, r13d", "mov edx, eax"} {
			if strings.Contains(body, stale) {
				t.Errorf("%s still sizes in 32 bits (%q):\n%s", name, stale, body)
			}
		}
	}
}

// The copy-on-write helpers read cap from a header the grow guard already
// accepted, so they need no check of their own — but the product must still
// be taken in 64 bits and handed to __fern_alloc whole, not truncated into edi.
func TestArrCowSizesIn64Bits(t *testing.T) {
	asm := compile(t, arrGrowProgram)
	for _, name := range []string{"__fern_arr_cow_inplace", "__fern_arr_cow_inplace_ptr"} {
		body := mustHelperSection(t, asm, name)
		for _, want := range []string{"imul rax, r12", "mov rdi, rax", "mov rdx, rax"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, body)
			}
		}
		for _, stale := range []string{"imul eax, r12d", "mov edi, eax", "mov edx, eax"} {
			if strings.Contains(body, stale) {
				t.Errorf("%s still sizes in 32 bits (%q):\n%s", name, stale, body)
			}
		}
	}
}

// mustHelperSection returns the emitted text of the named runtime helper, from
// its label to its `.size` directive — the whole symbol, including any
// out-of-line abort tail past the last `ret`. Fails the test when the helper
// was not emitted at all, since an assertion over missing text proves nothing.
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
