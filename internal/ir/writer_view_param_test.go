package ir

import "testing"

// Automatic array lending must preserve Writer's synchronous borrow contract.
// Otherwise a helper wrapping write_bytes loses its argument-reclaim summary,
// and callers such as BufWriter.flush strand the fresh array passed to it.
func TestWriterLentArrayParamRetainsCopyingCredit(t *testing.T) {
	for _, call := range []string{
		"stdout().write_bytes(p)",
		"stdout().write_some_bytes(p)",
		"stdout().write_bytes(p[1:2])",
		"stdout().write_some_bytes(p[1:2])",
	} {
		t.Run(call, func(t *testing.T) {
			src := "function send(p: u8[]): i32 { " + call + "; return 0; }\nfunction main(): i32 { return 0; }"
			got := paramCountedFor(t, src, "send")
			if len(got) != 1 || !got[0] {
				t.Fatalf("send retains no input, got parameter credit %v", got)
			}
		})
	}
}

func TestWriterLendingDoesNotCreditAnEscapingArrayView(t *testing.T) {
	for _, body := range []string{
		"stdout().write_bytes(p); return p[0:1];",
		"return keep(p);",
	} {
		t.Run(body, func(t *testing.T) {
			src := "function keep(v: [u8]): [u8] { return v; }\nfunction send(p: u8[]): [u8] { " + body + " }\nfunction main(): i32 { return 0; }"
			got := paramCountedFor(t, src, "send")
			if len(got) != 1 || got[0] {
				t.Fatalf("returned view still borrows its array, got parameter credit %v", got)
			}
		})
	}
}
