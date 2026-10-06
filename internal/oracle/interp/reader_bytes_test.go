package interp

import (
	"io"
	"syscall"
	"testing"
)

type readerBytesOutcome struct {
	data  []byte
	err   error
	calls int
}

func (r *readerBytesOutcome) Read(p []byte) (int, error) {
	r.calls++
	return copy(p, r.data), r.err
}

func TestReaderBytesHostOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, variant string
		data          []byte
		err           error
		count         Number
		closed        bool
		calls         int
	}{
		{name: "bytes with EOF", variant: "Ok", data: []byte{0, 255, 128}, err: io.EOF, count: 8, calls: 1},
		{name: "EOF", variant: "Ok", err: io.EOF, count: 8, calls: 1},
		{name: "read failure", variant: "Other", err: syscall.EIO, count: 8, calls: 1},
		{name: "interrupted", variant: "Interrupted", err: syscall.EINTR, count: 8, calls: 1},
		{name: "zero preserves interruption", variant: "Interrupted", err: syscall.EINTR, count: 0, calls: 1},
		{name: "negative does not read", variant: "Other", count: -1},
		{name: "closed does not read", variant: "Other", count: 8, closed: true},
		{name: "closed zero does not read", variant: "Other", count: 0, closed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := &readerBytesOutcome{data: tc.data, err: tc.err}
			i := New()
			i.Stdin = host
			i.closedStd = map[int64]bool{0: tc.closed}
			r := &Struct{TypeName: "Reader", Fields: map[string]Value{"fd": Number(0)}}
			got, err := builtinReaderReadChunkBytes(i, []Value{r, tc.count})
			if err != nil {
				t.Fatal(err)
			}
			res := got.(*Enum)
			if tc.variant == "Ok" {
				if res.VariantName != "Ok" {
					t.Fatalf("result = %#v", res)
				}
				bs := res.Payloads[0].(Array)
				if len(bs.E) != len(tc.data) {
					t.Fatalf("length = %d, want %d", len(bs.E), len(tc.data))
				}
				for n, b := range tc.data {
					if bs.E[n] != Number(b) {
						t.Fatalf("byte %d = %v, want %d", n, bs.E[n], b)
					}
				}
			} else if res.VariantName != "Err" || res.Payloads[0].(*Enum).VariantName != tc.variant {
				t.Fatalf("result = %#v, want Err(%s)", res, tc.variant)
			}
			if host.calls != tc.calls {
				t.Fatalf("host reads = %d, want %d", host.calls, tc.calls)
			}
		})
	}
}
