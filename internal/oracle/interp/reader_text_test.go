package interp

import (
	"io"
	"syscall"
	"testing"
)

func TestReaderTextHostOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, variant string
		data          []byte
		err           error
		count         Number
		closed        bool
		calls         int
	}{
		{name: "text with EOF", variant: "Ok", data: []byte("é\x00"), err: io.EOF, count: 8, calls: 1},
		{name: "text with error", variant: "Ok", data: []byte("é"), err: syscall.EIO, count: 8, calls: 1},
		{name: "invalid with EOF", variant: "InvalidUtf8", data: []byte{255}, err: io.EOF, count: 8, calls: 1},
		{name: "invalid with error", variant: "InvalidUtf8", data: []byte{195}, err: syscall.EIO, count: 8, calls: 1},
		{name: "EOF", variant: "Ok", err: io.EOF, count: 8, calls: 1},
		{name: "read failure", variant: "Other", err: syscall.EIO, count: 8, calls: 1},
		{name: "directory failure", variant: "Other", err: syscall.EISDIR, count: 8, calls: 1},
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
			got, err := builtinReaderReadChunk(i, []Value{r, tc.count})
			if err != nil {
				t.Fatal(err)
			}
			res := got.(*Enum)
			if tc.variant == "Ok" {
				if res.VariantName != "Ok" || res.Payloads[0] != String(string(tc.data)) {
					t.Fatalf("result = %#v, want Ok(%q)", res, tc.data)
				}
			} else if res.VariantName != "Err" || res.Payloads[0].(*Enum).VariantName != tc.variant {
				t.Fatalf("result = %#v, want Err(%s)", res, tc.variant)
			} else if tc.variant == "InvalidUtf8" && res.Payloads[0].(*Enum).Payloads[0] != String("") {
				t.Fatalf("invalid text error must carry an empty path: %#v", res)
			}
			if host.calls != tc.calls {
				t.Fatalf("host reads = %d, want %d", host.calls, tc.calls)
			}
		})
	}
}
