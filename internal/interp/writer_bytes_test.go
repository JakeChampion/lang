package interp

import (
	"bytes"
	"syscall"
	"testing"
)

type writerBytesHost struct {
	count int
	err   error
	calls int
	out   []byte
}

func (w *writerBytesHost) Write(p []byte) (int, error) {
	w.calls++
	n := min(w.count, len(p))
	w.out = append(w.out, p[:n]...)
	return n, w.err
}

func TestWriterBytesHostOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		all, closed, empty    bool
		count, calls, written int
		err                   error
		variant, problem      string
	}{
		{name: "short writes complete", all: true, count: 1, calls: 3, written: 3, variant: "None"},
		{name: "one short write", count: 1, calls: 1, written: 1, variant: "Ok"},
		{name: "zero is a count", count: 0, calls: 1, variant: "Ok"},
		{name: "zero cannot spin", all: true, count: 0, calls: 1, variant: "Some", problem: "Other"},
		{name: "some interrupted", count: 0, calls: 1, err: syscall.EINTR, variant: "Err", problem: "Interrupted"},
		{name: "all interrupted", all: true, count: 0, calls: 1, err: syscall.EINTR, variant: "Some", problem: "Interrupted"},
		{name: "empty preserves error", all: true, empty: true, calls: 1, err: syscall.EIO, variant: "Some", problem: "Other"},
		{name: "empty host write", all: true, empty: true, calls: 1, variant: "None"},
		{name: "closed", all: true, closed: true, variant: "Some", problem: "Other"},
		{name: "closed empty", empty: true, closed: true, variant: "Err", problem: "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &writerBytesHost{count: tc.count, err: tc.err}
			i := New()
			i.Stdout = host
			i.closedStd = map[int64]bool{1: tc.closed}
			a := Array{E: []Value{Number(0), Number(255), Number(128)}}
			expected := []byte{0, 255, 128}
			if tc.empty {
				a.E = nil
				expected = nil
			}
			w := &Struct{TypeName: "Writer", Fields: map[string]Value{"fd": Number(1)}}
			v, e := writerBytes(i, []Value{w, a}, tc.all)
			if e != nil {
				t.Fatal(e)
			}
			r := v.(*Enum)
			if r.VariantName != tc.variant {
				t.Fatalf("result=%#v", r)
			}
			if tc.problem != "" && r.Payloads[0].(*Enum).VariantName != tc.problem {
				t.Fatalf("error=%#v", r.Payloads[0])
			}
			if tc.variant == "Ok" && r.Payloads[0] != Number(tc.written) {
				t.Fatalf("count=%v", r.Payloads[0])
			}
			if host.calls != tc.calls || !bytes.Equal(host.out, expected[:tc.written]) {
				t.Fatalf("calls=%d output=%v", host.calls, host.out)
			}
			for n, v := range a.E {
				if v != Number(expected[n]) {
					t.Fatal("input changed")
				}
			}
		})
	}
}
